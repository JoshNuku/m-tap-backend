-- 000001_init_schema.up.sql
-- M-Tap Micro-Retail Switching Database Schema
-- Optimized for PostgreSQL 16 on Neon Serverless (PgBouncer mode)

CREATE EXTENSION IF NOT EXISTS "pgcrypto";

-- 1. Merchants Table
CREATE TABLE IF NOT EXISTS merchants (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_code VARCHAR(32) NOT NULL UNIQUE,
    business_name VARCHAR(128) NOT NULL,
    settlement_msisdn VARCHAR(16) NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'ACTIVE',
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_merchants_code ON merchants(merchant_code);

-- 2. Terminals Table
CREATE TABLE IF NOT EXISTS terminals (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    merchant_id UUID NOT NULL REFERENCES merchants(id) ON DELETE RESTRICT,
    terminal_identifier VARCHAR(32) NOT NULL UNIQUE,
    hmac_secret VARCHAR(128) NOT NULL,
    status VARCHAR(16) NOT NULL DEFAULT 'ACTIVE',
    last_heartbeat TIMESTAMPTZ,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_terminals_identifier ON terminals(terminal_identifier);

-- 3. Customers Table
CREATE TABLE IF NOT EXISTS customers (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    phone_number VARCHAR(16) NOT NULL UNIQUE,
    daily_limit_pesewas BIGINT NOT NULL DEFAULT 50000, -- GHS 500.00
    is_blocked BOOLEAN NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_customers_phone ON customers(phone_number);

-- 4. Customer Tags Table
CREATE TABLE IF NOT EXISTS customer_tags (
    tag_uid VARCHAR(14) PRIMARY KEY, -- 7-byte hex string (e.g. 04A38CF2196880)
    customer_id UUID REFERENCES customers(id) ON DELETE SET NULL,
    status VARCHAR(20) NOT NULL DEFAULT 'PENDING_ACTIVATION', -- PENDING_ACTIVATION, ACTIVE, FROZEN, REVOKED
    failed_prompt_count INT NOT NULL DEFAULT 0,
    activated_at TIMESTAMPTZ,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_customer_tags_status ON customer_tags(status);
CREATE INDEX IF NOT EXISTS idx_customer_tags_customer ON customer_tags(customer_id);

-- 5. Transactions Ledger Table
CREATE TABLE IF NOT EXISTS transactions (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    terminal_id UUID NOT NULL REFERENCES terminals(id) ON DELETE RESTRICT,
    tag_uid VARCHAR(14) NOT NULL REFERENCES customer_tags(tag_uid) ON DELETE RESTRICT,
    amount_pesewas BIGINT NOT NULL,
    external_reference VARCHAR(64) NOT NULL UNIQUE,
    settlement_state VARCHAR(24) NOT NULL DEFAULT 'PENDING', -- PENDING, SETTLED, DECLINED, TIMEOUT
    latency_ms INT,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    finalized_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_transactions_ref ON transactions(external_reference);
CREATE INDEX IF NOT EXISTS idx_transactions_state ON transactions(settlement_state);
CREATE INDEX IF NOT EXISTS idx_transactions_created_at ON transactions(created_at DESC);
CREATE INDEX IF NOT EXISTS idx_transactions_tag_uid ON transactions(tag_uid);

-- 6. Audit Logs Table (Append-Only Compliance & Forensics Ledger)
CREATE TABLE IF NOT EXISTS audit_logs (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    entity_type VARCHAR(32) NOT NULL, -- CUSTOMER_TAG, TERMINAL, TRANSACTION
    entity_id VARCHAR(64) NOT NULL,   -- tag_uid, terminal_id, or transaction UUID
    action VARCHAR(32) NOT NULL,      -- TAG_PAIR_INITIATED, TAG_ACTIVATED, TAG_FROZEN, TAG_REVOKED, AUTH_FAILED, etc.
    actor_type VARCHAR(16) NOT NULL,  -- CUSTOMER, MERCHANT, SYSTEM_RULE, ADMIN
    actor_id VARCHAR(64),             -- Phone number, terminal identifier, or rule engine ID
    metadata JSONB NOT NULL DEFAULT '{}'::jsonb,
    created_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);
CREATE INDEX IF NOT EXISTS idx_audit_logs_entity ON audit_logs (entity_type, entity_id);
CREATE INDEX IF NOT EXISTS idx_audit_logs_action ON audit_logs (action);
CREATE INDEX IF NOT EXISTS idx_audit_logs_created_at ON audit_logs (created_at DESC);

