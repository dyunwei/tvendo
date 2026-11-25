package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
)

func parseTrustedProxies(raw string) ([]netip.Prefix, error) {
	var proxies []netip.Prefix
	if strings.TrimSpace(raw) == "" {
		return proxies, nil
	}
	for _, value := range strings.Split(raw, ",") {
		prefix, err := netip.ParsePrefix(strings.TrimSpace(value))
		if err != nil || prefix.Bits() == 0 || prefix.Addr().Is4In6() {
			return nil, fmt.Errorf("invalid TRUSTED_PROXY_CIDRS entry %q", value)
		}
		proxies = append(proxies, prefix.Masked())
	}
	return proxies, nil
}

func (a *App) trustedProxy(addr netip.Addr) bool {
	for _, prefix := range a.trustedProxies {
		if prefix.Contains(addr.Unmap()) {
			return true
		}
	}
	return false
}

func (a *App) clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		host = r.RemoteAddr
	}
	peer, err := netip.ParseAddr(host)
	if err != nil {
		return "unknown"
	}
	if !a.trustedProxy(peer) {
		return peer.Unmap().String()
	}
	forwarded := strings.Split(strings.Join(r.Header.Values("X-Forwarded-For"), ","), ",")
	for i := len(forwarded) - 1; i >= 0; i-- {
		addr, err := netip.ParseAddr(strings.TrimSpace(forwarded[i]))
		if err != nil || addr.Zone() != "" {
			break
		}
		if !a.trustedProxy(addr) {
			return addr.Unmap().String()
		}
	}
	return peer.Unmap().String()
}

type trafficLimit struct {
	scope  string
	max    int
	window time.Duration
}

const minute = time.Minute

// Check and charge all buckets together. A rejected request does not drain
// another user's quota or a shared bucket.
var admitTraffic = redis.NewScript(`
local now = tonumber(redis.call('TIME')[1])
local entries = {}
for i, prefix in ipairs(KEYS) do
	local window = tonumber(ARGV[2*i])
	local key = prefix .. ':' .. math.floor(now/window)
	local max = tonumber(ARGV[2*i-1])
	local current = tonumber(redis.call('GET', key) or '0')
	if current >= max then return window-now%window end
	entries[i] = {key, window}
end
for _, entry in ipairs(entries) do
	if redis.call('INCR', entry[1]) == 1 then
		redis.call('EXPIRE', entry[1], entry[2])
	end
end
return 0`)

func (a *App) rateAllowed(w http.ResponseWriter, r *http.Request, limits ...trafficLimit) bool {
	keys := make([]string, 0, len(limits))
	args := make([]any, 0, 2*len(limits))
	for _, limit := range limits {
		keys = append(keys, "traffic:rate:"+limit.scope)
		args = append(args, limit.max, int(limit.window/time.Second))
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	retry, err := admitTraffic.Run(ctx, a.redis, keys, args...).Int()
	if err != nil {
		log.Printf("rate limiter unavailable: %v", err)
		fail(w, 503, "请求限流服务暂不可用")
		return false
	}
	if retry > 0 {
		w.Header().Set("Retry-After", strconv.Itoa(retry))
		fail(w, 429, "操作过于频繁，请稍后重试")
		return false
	}
	return true
}

func requestLimits(kind string, u User) []trafficLimit {
	account := kind + ":user:" + strconv.FormatInt(u.ID, 10)
	key := kind + ":key:" + strconv.FormatInt(u.KeyID, 10)
	var limits []trafficLimit
	switch kind {
	case "run":
		limits = append(limits, trafficLimit{account, 120, minute}, trafficLimit{kind + ":all", 900, minute})
		if u.KeyID != 0 {
			limits = append(limits, trafficLimit{key, 90, minute})
		}
	case "search":
		limits = append(limits, trafficLimit{account, 60, minute}, trafficLimit{kind + ":all", 480, minute})
		if u.KeyID != 0 {
			limits = append(limits, trafficLimit{key, 40, minute})
		}
	case "models":
		limits = append(limits, trafficLimit{account, 500, 24 * time.Hour}, trafficLimit{kind + ":all", 5000, 24 * time.Hour})
	}
	return limits
}

var acquireTraffic = redis.NewScript(`
local now = tonumber(redis.call('TIME')[1])
for i, key in ipairs(KEYS) do
	redis.call('ZREMRANGEBYSCORE', key, '-inf', now)
	if redis.call('ZCARD', key) >= tonumber(ARGV[i+1]) then return 0 end
end
for _, key in ipairs(KEYS) do
	redis.call('ZADD', key, now+120, ARGV[1])
	redis.call('EXPIRE', key, 120)
end
return 1`)

var releaseTraffic = redis.NewScript(`
for _, key in ipairs(KEYS) do redis.call('ZREM', key, ARGV[1]) end
return 1`)

func (a *App) reserveConcurrency(w http.ResponseWriter, r *http.Request, kind string, userID int64, globalMax, userMax int) (release func(), ok bool) {
	prefix := "traffic:concurrency:" + kind
	keys := []string{prefix + ":all", prefix + ":user:" + strconv.FormatInt(userID, 10)}
	token := randomToken()
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	allowed, err := acquireTraffic.Run(ctx, a.redis, keys, token, globalMax, userMax).Int()
	if err != nil {
		log.Printf("concurrency limiter unavailable: %v", err)
		fail(w, 503, "并发限流服务暂不可用")
		return nil, false
	}
	if allowed == 0 {
		w.Header().Set("Retry-After", "1")
		fail(w, 429, "并发请求已达到上限，请稍后重试")
		return nil, false
	}
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := releaseTraffic.Run(ctx, a.redis, keys, token).Err(); err != nil {
			log.Printf("release concurrency slot: %v", err)
		}
	}, true
}
