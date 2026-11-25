-- Monetary values are integer micro-yuan: 1 CNY = 1,000,000.
CREATE TABLE IF NOT EXISTS users (
 id BIGSERIAL PRIMARY KEY, email TEXT UNIQUE NOT NULL, name TEXT NOT NULL,
 password TEXT NOT NULL, role TEXT NOT NULL DEFAULT 'user', balance BIGINT NOT NULL DEFAULT 0 CHECK(balance>=0),
 daily_limit BIGINT NOT NULL DEFAULT 3000000, call_limit BIGINT NOT NULL DEFAULT 300000,
 daily_enabled BOOLEAN NOT NULL DEFAULT false, call_enabled BOOLEAN NOT NULL DEFAULT false, created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS single_admin ON users(role) WHERE role='admin';
CREATE TABLE IF NOT EXISTS api_keys (
 id BIGSERIAL PRIMARY KEY, user_id BIGINT REFERENCES users(id), label TEXT NOT NULL, hash TEXT UNIQUE NOT NULL,
 prefix TEXT NOT NULL, revoked BOOLEAN NOT NULL DEFAULT false, last_used TIMESTAMPTZ, created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS tools (
 id BIGSERIAL PRIMARY KEY, slug TEXT UNIQUE NOT NULL, name TEXT NOT NULL, category TEXT NOT NULL,
 description TEXT NOT NULL DEFAULT '', website TEXT NOT NULL DEFAULT '', docs TEXT NOT NULL DEFAULT '',
 icon TEXT NOT NULL DEFAULT '✦', base_url TEXT NOT NULL DEFAULT '', auth_header TEXT NOT NULL DEFAULT 'Authorization',
 auth_prefix TEXT NOT NULL DEFAULT 'Bearer ', secret TEXT NOT NULL DEFAULT '', enabled BOOLEAN NOT NULL DEFAULT false,
 endpoints JSONB NOT NULL DEFAULT '[]', resources JSONB NOT NULL DEFAULT '[]', created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS usage (
 id BIGSERIAL PRIMARY KEY, user_id BIGINT REFERENCES users(id), tool_name TEXT NOT NULL,
 endpoint TEXT NOT NULL, cost BIGINT NOT NULL, status TEXT NOT NULL DEFAULT 'running',
 http_status INT NOT NULL DEFAULT 0, duration_ms BIGINT NOT NULL DEFAULT 0, created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS usage_user_time ON usage(user_id, created_at);
CREATE TABLE IF NOT EXISTS transactions (
 id BIGSERIAL PRIMARY KEY, user_id BIGINT REFERENCES users(id), amount BIGINT NOT NULL,
 kind TEXT NOT NULL, note TEXT NOT NULL DEFAULT '', actor_id BIGINT REFERENCES users(id), created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE TABLE IF NOT EXISTS audit_log (
 id BIGSERIAL PRIMARY KEY, actor_id BIGINT REFERENCES users(id), action TEXT NOT NULL, target_id BIGINT NOT NULL, created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
-- Additive upgrades for existing deployments; no data reset required.
ALTER TABLE tools ADD COLUMN IF NOT EXISTS upstream_type TEXT NOT NULL DEFAULT 'http';
ALTER TABLE tools ADD COLUMN IF NOT EXISTS default_price_micros BIGINT NOT NULL DEFAULT 0;
ALTER TABLE tools ADD COLUMN IF NOT EXISTS revision BIGINT NOT NULL DEFAULT 1;
ALTER TABLE tools ADD COLUMN IF NOT EXISTS last_synced_at TIMESTAMPTZ;

ALTER TABLE usage ADD COLUMN IF NOT EXISTS request_id TEXT NOT NULL DEFAULT '';
ALTER TABLE usage ADD COLUMN IF NOT EXISTS key_id BIGINT REFERENCES api_keys(id);
ALTER TABLE usage ADD COLUMN IF NOT EXISTS key_label TEXT NOT NULL DEFAULT '';
CREATE INDEX IF NOT EXISTS usage_user_request ON usage(user_id, request_id);

ALTER TABLE usage ADD COLUMN IF NOT EXISTS error_code TEXT NOT NULL DEFAULT '';
ALTER TABLE usage ADD COLUMN IF NOT EXISTS error_message TEXT NOT NULL DEFAULT '';

ALTER TABLE usage ADD COLUMN IF NOT EXISTS idempotency_key TEXT;
ALTER TABLE usage ADD COLUMN IF NOT EXISTS request_fingerprint TEXT;
ALTER TABLE usage ADD COLUMN IF NOT EXISTS resolution_note TEXT NOT NULL DEFAULT '';
ALTER TABLE usage ADD COLUMN IF NOT EXISTS resolved_at TIMESTAMPTZ;
CREATE INDEX IF NOT EXISTS usage_running_recovery ON usage(created_at) WHERE status='running';
CREATE UNIQUE INDEX IF NOT EXISTS usage_user_idempotency ON usage(user_id, idempotency_key)
 WHERE idempotency_key IS NOT NULL;
CREATE TABLE IF NOT EXISTS run_results (
 run_id BIGINT PRIMARY KEY REFERENCES usage(id) ON DELETE CASCADE,
 encrypted_data TEXT NOT NULL,
 expires_at TIMESTAMPTZ NOT NULL
);
CREATE INDEX IF NOT EXISTS run_results_expiry ON run_results(expires_at);

-- One vector per endpoint/model configuration. Hashes invalidate changed descriptions;
-- a model revision change builds a separate index without serving old vectors.
CREATE TABLE IF NOT EXISTS catalog_embeddings (
 tool_id BIGINT NOT NULL REFERENCES tools(id) ON DELETE CASCADE,
 endpoint_id TEXT NOT NULL,
 model_key TEXT NOT NULL,
 document_hash TEXT NOT NULL,
 embedding DOUBLE PRECISION[] NOT NULL,
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 PRIMARY KEY (tool_id, endpoint_id, model_key)
);
CREATE INDEX IF NOT EXISTS catalog_embeddings_model ON catalog_embeddings(model_key);

-- Online payments are separate from ledger entries. Only verified payments credit balances.
CREATE TABLE IF NOT EXISTS payment_orders (
 id TEXT PRIMARY KEY, user_id BIGINT NOT NULL REFERENCES users(id),
 request_key TEXT NOT NULL, appid TEXT NOT NULL, mch_id TEXT NOT NULL,
 amount_cents BIGINT NOT NULL CHECK (amount_cents IN (1,1000,5000,10000,20000)),
 status TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending','paid','closed')),
 code_url TEXT NOT NULL DEFAULT '', transaction_id TEXT UNIQUE,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(), expires_at TIMESTAMPTZ NOT NULL,
 paid_at TIMESTAMPTZ, next_check_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 UNIQUE(user_id,request_key)
);
CREATE INDEX IF NOT EXISTS payment_orders_user ON payment_orders(user_id,created_at DESC);
CREATE INDEX IF NOT EXISTS payment_orders_recovery ON payment_orders(next_check_at) WHERE status='pending';
ALTER TABLE transactions ADD COLUMN IF NOT EXISTS payment_order_id TEXT REFERENCES payment_orders(id);
CREATE UNIQUE INDEX IF NOT EXISTS transactions_payment_order ON transactions(payment_order_id) WHERE payment_order_id IS NOT NULL;

-- Upgrade existing payment tables to permit a one-cent payment for live testing.
ALTER TABLE payment_orders
 DROP CONSTRAINT IF EXISTS payment_orders_amount_cents_check,
 ADD CONSTRAINT payment_orders_amount_cents_check CHECK (amount_cents IN (1,1000,5000,10000,20000));

CREATE TABLE IF NOT EXISTS payment_refunds (
 order_id TEXT PRIMARY KEY REFERENCES payment_orders(id),
 actor_id BIGINT NOT NULL REFERENCES users(id),
 reason TEXT NOT NULL,
 status TEXT NOT NULL DEFAULT 'queued' CHECK (status IN ('queued','processing','abnormal','succeeded','closed')),
 wechat_refund_id TEXT UNIQUE,
 created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 succeeded_at TIMESTAMPTZ,
 next_check_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS payment_refunds_recovery ON payment_refunds(next_check_at)
 WHERE status IN ('queued','processing','abnormal');
ALTER TABLE transactions ADD COLUMN IF NOT EXISTS refund_order_id TEXT REFERENCES payment_refunds(order_id);
CREATE UNIQUE INDEX IF NOT EXISTS transactions_payment_refund ON transactions(refund_order_id) WHERE refund_order_id IS NOT NULL;
CREATE INDEX IF NOT EXISTS payment_orders_paid_at ON payment_orders(mch_id,appid,paid_at) WHERE status='paid';
CREATE INDEX IF NOT EXISTS payment_refunds_succeeded_at ON payment_refunds(succeeded_at) WHERE status='succeeded';
CREATE TABLE IF NOT EXISTS payment_bill_reports (
 merchant_id TEXT NOT NULL,
 app_id TEXT NOT NULL,
 bill_date DATE NOT NULL,
 checked_at TIMESTAMPTZ NOT NULL DEFAULT now(),
 matched INT NOT NULL,
 difference_count INT NOT NULL,
 differences JSONB NOT NULL,
 PRIMARY KEY (merchant_id,app_id,bill_date)
);
