package main

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/pem"
	"math/big"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wechatpay-apiv3/wechatpay-go/utils"
)

func testPlatformCertificate(t *testing.T, key *rsa.PrivateKey, serial int64, from, to time.Time) []byte {
	t.Helper()
	template := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "Test WeChat Pay platform"}, NotBefore: from, NotAfter: to, KeyUsage: x509.KeyUsageDigitalSignature}
	der, e := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if e != nil {
		t.Fatal(e)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}
func testPaymentFile(t *testing.T, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if e := os.WriteFile(path, data, 0600); e != nil {
		t.Fatal(e)
	}
	return path
}
func configureTestPayment(t *testing.T, key *rsa.PrivateKey) {
	t.Helper()
	privateDER, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	for k, v := range map[string]string{
		"WECHAT_PAY_NOTIFY_URL": "https://payments.example.com/api/payments/wechat/notify",
		"WECHAT_PAY_ENABLED":    "true", "WECHAT_PAY_APP_ID": "test-app", "WECHAT_PAY_MCH_ID": "test-merchant", "WECHAT_PAY_API_V3_KEY": strings.Repeat("k", 32), "WECHAT_PAY_SERIAL_NO": "MERCHANT123",
		"WECHAT_PAY_PRIVATE_KEY_PATH": testPaymentFile(t, "merchant.pem", pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privateDER})),
		"WECHAT_PAY_PUBLIC_KEY_ID":    "", "WECHAT_PAY_PUBLIC_KEY_PATH": "", "WECHAT_PAY_PLATFORM_CERT_PATH": "",
	} {
		t.Setenv(k, v)
	}
}
func TestWechatPaymentVerificationModes(t *testing.T) {
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now()
	certPEM := testPlatformCertificate(t, key, 0x1234, now.Add(-time.Hour), now.Add(time.Hour))
	certificatePath := testPaymentFile(t, "wechatpay_platform.pem", certPEM)
	for _, mode := range []string{"certificate", "public_key"} {
		t.Run(mode, func(t *testing.T) {
			configureTestPayment(t, key)
			t.Setenv("WECHAT_PAY_VERIFY_MODE", mode)
			serial := "PUB_KEY_ID_test"
			if mode == "certificate" {
				serial = "1234"
				t.Setenv("WECHAT_PAY_PLATFORM_CERT_PATH", certificatePath)
			} else {
				der, e := x509.MarshalPKIXPublicKey(&key.PublicKey)
				if e != nil {
					t.Fatal(e)
				}
				t.Setenv("WECHAT_PAY_PUBLIC_KEY_ID", serial)
				t.Setenv("WECHAT_PAY_PUBLIC_KEY_PATH", testPaymentFile(t, "public.pem", pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})))
			}
			t.Setenv("PUBLIC_URL", "http://localhost:8080")
			payment, e := newWechatPayment(context.Background())
			if e != nil {
				t.Fatal(e)
			}
			if got := payment.gateway.(*nativeGateway).notifyURL; got != "https://payments.example.com/api/payments/wechat/notify" {
				t.Fatalf("callback URL changed: %s", got)
			}
			// Exercise the actual client's response validator, not only the verifier in isolation.
			var tamper atomic.Bool
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if !strings.Contains(r.Header.Get("Authorization"), `serial_no="MERCHANT123"`) {
					t.Error("merchant signing credentials missing")
				}
				body := `{"ok":true}`
				timestamp := big.NewInt(time.Now().Unix()).String()
				digest := sha256.Sum256([]byte(timestamp + "\nnonce\n" + body + "\n"))
				signature, e := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
				if e != nil {
					t.Error(e)
					return
				}
				w.Header().Set("Wechatpay-Serial", serial)
				w.Header().Set("Wechatpay-Timestamp", timestamp)
				w.Header().Set("Wechatpay-Nonce", "nonce")
				w.Header().Set("Wechatpay-Signature", base64.StdEncoding.EncodeToString(signature))
				w.Header().Set("Content-Type", "application/json")
				if tamper.Load() {
					body = `{"ok":false}`
				}
				w.Write([]byte(body))
			}))
			defer server.Close()
			client := payment.gateway.(*nativeGateway).svc.Client
			result, e := client.Request(context.Background(), "GET", server.URL, nil, nil, nil, "")
			if e != nil {
				t.Fatalf("valid signed response rejected: %v", e)
			}
			result.Response.Body.Close()
			tamper.Store(true)
			if _, e = client.Request(context.Background(), "GET", server.URL, nil, nil, nil, ""); e == nil {
				t.Fatal("tampered API response accepted")
			}
		})
	}
}
func TestPlatformCertificateConfiguration(t *testing.T) {
	key, e := rsa.GenerateKey(rand.Reader, 2048)
	if e != nil {
		t.Fatal(e)
	}
	now := time.Now()
	valid := testPlatformCertificate(t, key, 1, now.Add(-time.Hour), now.Add(time.Hour))
	configureTestPayment(t, key)
	t.Setenv("WECHAT_PAY_VERIFY_MODE", "certificate")
	for _, c := range []struct {
		name string
		data []byte
	}{
		{"empty", nil}, {"malformed", []byte("not a certificate")}, {"public key", pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: []byte("bad")})},
		{"expired", testPlatformCertificate(t, key, 2, now.Add(-2*time.Hour), now.Add(-time.Hour))},
		{"future", testPlatformCertificate(t, key, 3, now.Add(time.Hour), now.Add(2*time.Hour))},
		{"trailing junk", append(append([]byte{}, valid...), []byte("invalid trailing data")...)},
	} {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("WECHAT_PAY_PLATFORM_CERT_PATH", testPaymentFile(t, "platform.pem", c.data))
			if _, e := configuredPaymentVerifier(); e == nil {
				t.Fatal("invalid certificate accepted")
			}
		})
	}
	t.Run("missing path", func(t *testing.T) {
		t.Setenv("WECHAT_PAY_PLATFORM_CERT_PATH", "")
		if _, e := configuredPaymentVerifier(); e == nil {
			t.Fatal("missing certificate accepted")
		}
	})
	t.Run("invalid mode", func(t *testing.T) {
		t.Setenv("WECHAT_PAY_VERIFY_MODE", "typo")
		if _, e := configuredPaymentVerifier(); e == nil {
			t.Fatal("unknown mode accepted")
		}
	})
	t.Run("merchant certificate mistaken for platform certificate", func(t *testing.T) {
		t.Setenv("WECHAT_PAY_PLATFORM_CERT_PATH", testPaymentFile(t, "apiclient_cert.pem", valid))
		t.Setenv("WECHAT_PAY_SERIAL_NO", "01")
		if _, e := configuredPaymentVerifier(); e == nil || !strings.Contains(e.Error(), "merchant API certificate") {
			t.Fatalf("merchant certificate accepted: %v", e)
		}
	})

	t.Run("bundle and expiry at verification", func(t *testing.T) {
		bundle := append(append([]byte{}, valid...), testPlatformCertificate(t, key, 4, now.Add(-time.Hour), now.Add(2*time.Hour))...)
		t.Setenv("WECHAT_PAY_PLATFORM_CERT_PATH", testPaymentFile(t, "bundle.pem", bundle))
		v, e := configuredPaymentVerifier()
		if e != nil {
			t.Fatal(e)
		}
		digest := sha256.Sum256([]byte("message"))
		sig, e := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, digest[:])
		if e != nil {
			t.Fatal(e)
		}
		encoded := base64.StdEncoding.EncodeToString(sig)
		for _, serial := range []string{"01", "04"} {
			if e = v.Verify(context.Background(), serial, "message", encoded); e != nil {
				t.Fatal(e)
			}
		}
		if e = v.Verify(context.Background(), "unknown", "message", encoded); e == nil {
			t.Fatal("unknown serial accepted")
		}
		if e = v.Verify(context.Background(), "01", "tampered", encoded); e == nil {
			t.Fatal("bad signature accepted")
		}
		cert, _ := v.(*platformCertificateVerifier).certs.Get(context.Background(), "01")
		cert.NotAfter = now.Add(-time.Second)
		if e = v.Verify(context.Background(), utils.GetCertificateSerialNumber(*cert), "message", encoded); e == nil {
			t.Fatal("expired certificate accepted after startup")
		}
	})
}
