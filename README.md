# M-Tap: Proximity Micro-Retail Switching Backend
**High-Throughput Distributed Switching Engine & Double-Entry Ledger**  
*Built by Boron Labs*

---

## 1. System Overview

**M-Tap** is an ultra-low-latency proximity micro-retail payment switching engine built for Ghanaian and African informal commerce. It bridges physical 7-byte NFC tags (`tag_uid`), hardware edge POS terminals, Mobile Money clearing rails (Paystack), and SMS onboarding networks (Africa's Talking) with strict sub-50ms switching SLAs and double-entry accounting guarantees.

### Core Architectural Stack
- **Runtime:** Go 1.22+ (Strict idiomatic Go, clean DDD layered architecture, structured logging with `slog`).
- **HTTP Routing:** Gin (`github.com/gin-gonic/gin`).
- **Interactive Documentation:** Embedded Swagger UI (`/swagger` or `/docs`) powered by OpenAPI 3.0 (`api/openapi.yaml`).
- **Relational Storage:** PostgreSQL 16 hosted on **Neon Serverless** (`github.com/jackc/pgx/v5` with `pgxpool`, configured for pooled connection lifetime and idle bounds in PgBouncer mode on port 6543).
- **Cache & Velocity Counter:** **Redis Cloud** (`github.com/redis/go-redis/v9`).
- **Asynchronous Task Queue:** **Asynq** (`github.com/hibiken/asynq`) backed by Redis running 3 priority queues:
  - `payments` (Priority: 6) — Transaction settlement & row-level locked balance updates.
  - `notifications` (Priority: 3) — Async Africa's Talking SMS delivery with exponential retries.
  - `default` (Priority: 1) — Routine auditing & metrics flushing.
- **Clearing Rail:** Paystack Mobile Money API (`POST /charge`) and asynchronous webhook listener.
- **SMS Gateway:** Africa's Talking SMS API (`/version1/messaging`).
- **Monetary Accounting:** All financial transactions and limits are strictly represented in minor units (**Pesewas**: 64-bit signed integers `int64`). Floating-point math is strictly forbidden.

---

## 2. Security & Defense Matrix

| Defense Vector | Implementation & Rules | Failure Outcome |
| :--- | :--- | :--- |
| **Terminal Edge Authentication** | HMAC-SHA256(`PreSharedKey`, `timestamp + "." + raw_body`) validated using `hmac.Equal` constant-time comparison. | `401 Unauthorized` (`INVALID_SIGNATURE`) |
| **Anti-Replay Window** | Rejects requests where $\| \text{now} - X\text{-Timestamp} \| > 30\text{s}$. | `401 Unauthorized` (`REPLAY_ATTACK_DETECTED`) |
| **Distributed Idempotency** | Atomic Redis acquisition `SET lock:idempotency:{key} "PROCESSING" NX EX 15s`. Handles in-flight taps. | `409 Conflict` (`CONCURRENT_TAP_DETECTED`) |
| **Per-Terminal Rate Limit** | Max 1 tap per 3 seconds per terminal ID (`ratelimit:terminal:{id}`). | `429 Too Many Requests` |
| **Tag Velocity Defense** | Redis counter `velocity:tag:{tag_uid}` (10m sliding window). If 3 failed attempts occur, tag is auto-frozen in Postgres, Redis cache is evicted, and audit entry recorded. | Tag state transitioned to `FROZEN` |
| **SMS Onboarding Guard** | Max 1 OTP SMS per 60 seconds per phone number; max 5 OTPs per IP per hour. | `429 Too Many Requests` |
| **Paystack Webhook Defense** | `x-paystack-signature` verified using HMAC-SHA512 over raw body bytes; IP whitelist (`52.31.139.75`, `52.49.173.169`, `52.214.14.220`). | `401 Unauthorized` / `403 Forbidden` |

---

## 3. Database Schema

Schema migrations are embedded directly in Go (`embed.FS`) and execute idempotently on startup.

1. **`merchants`**: Store IDs, unique merchant codes, business names, and settlement MSISDN.
2. **`terminals`**: POS hardware registered to merchants, holding pre-shared HMAC keys (`hmac_secret`).
3. **`customers`**: Registered phone numbers (E.164), daily spending limits (Pesewas), blocked flags.
4. **`customer_tags`**: 7-byte hex string (`tag_uid`), linked customer, status (`PENDING_ACTIVATION`, `ACTIVE`, `FROZEN`, `REVOKED`), failed count.
5. **`transactions`**: Terminal, tag, minor unit amount (`amount_pesewas`), unique external reference, settlement state (`PENDING`, `SETTLED`, `DECLINED`, `TIMEOUT`), latency.
6. **`audit_logs`**: Append-only forensic journal tracking entities (`CUSTOMER_TAG`, `TERMINAL`, `TRANSACTION`), actors (`CUSTOMER`, `MERCHANT`, `TERMINAL`, `SYSTEM_RULE`), actions, and JSONB metadata.

---

## 4. Redis Key Patterns & Cache Strategy

- `tag:active:{tag_uid}` $\rightarrow$ String (`phone_number`), TTL: 24 Hours. (Sub-2ms cache for counter taps).
- `otp:{tag_uid}` $\rightarrow$ JSON (`{"phone_number": "...", "hashed_otp": "..."}`), TTL: 300 seconds (5 mins).
- `lock:idempotency:{key}` $\rightarrow$ String (`PROCESSING`), TTL: 15 seconds.
- `velocity:tag:{tag_uid}` $\rightarrow$ Integer count, TTL: 600 seconds (10 mins).
- `ratelimit:terminal:{terminal_id}` $\rightarrow$ String (`1`), TTL: 3 seconds.
- `ratelimit:sms:phone:{phone_number}` $\rightarrow$ String (`1`), TTL: 60 seconds.
- `ratelimit:sms:ip:{ip}` $\rightarrow$ Integer counter, TTL: 3600 seconds (1 hour).

---

## 5. Quickstart & Bench Testing

### Option A: Local Sandbox (Zero External Dependencies)
Start PostgreSQL 16 and Redis 7 in Docker:
```bash
docker compose up -d postgres redis
```

Run database migrations and seed default merchant, terminal, and active tag:
```bash
go run cmd/seed/main.go
```

Start the M-Tap switching backend:
```bash
go run cmd/server/main.go
```

### Option B: Cloud Deployment (Neon Serverless & Redis Cloud)
Set the following in `.env`:
```env
DATABASE_URL=postgres://user:password@ep-cool-project.us-east-2.aws.neon.tech:6543/neondb?sslmode=require
REDIS_URL=redis://default:cloud_password@redis-12345.c10.us-east-1.ec2.redns.redis-cloud.com:12345
PAYSTACK_SECRET_KEY=sk_live_...
AFRICASTALKING_USERNAME=your_username
AFRICASTALKING_API_KEY=atsk_...
MOCK_PAYSTACK_SUCCESS=false
MOCK_SMS=false
```

---

## 6. API Walkthrough & Verification

### Interactive Swagger UI & OpenAPI Specification
- **Live Render Dashboard:** [https://m-tap-backend.onrender.com/swagger](https://m-tap-backend.onrender.com/swagger) (or `/docs`)
- **Live OpenAPI 3.0 Spec:** [https://m-tap-backend.onrender.com/swagger/openapi.yaml](https://m-tap-backend.onrender.com/swagger/openapi.yaml)
- **Liveness & Readiness Probes:**
  - Health: `https://m-tap-backend.onrender.com/health`
  - Ready: `https://m-tap-backend.onrender.com/ready`
- **Local Sandbox (when running locally):** [http://localhost:8080/swagger](http://localhost:8080/swagger)

---

### A. Terminal Tap Operation (`POST /api/v1/terminal/tap`)
Protected by HMAC-SHA256 signature, anti-replay window, distributed idempotency, and terminal rate limiting.

#### Generating the Signature:
For terminal `POD_001` with pre-shared key `secret_hmac_psk_key_boron_labs_2026`:
```bash
TIMESTAMP=$(date +%s)
BODY='{"terminal_id":"POD_001","tag_uid":"04A38CF2196880","amount_pesewas":2500}'
SIGNATURE=$(echo -n "${TIMESTAMP}.${BODY}" | openssl dgst -sha256 -hmac "secret_hmac_psk_key_boron_labs_2026" | awk '{print $2}')
```

#### Executing the Tap (Live on Render):
```bash
curl -i -X POST https://m-tap-backend.onrender.com/api/v1/terminal/tap \
  -H "Content-Type: application/json" \
  -H "X-Terminal-Id: POD_001" \
  -H "X-Timestamp: ${TIMESTAMP}" \
  -H "X-Signature: ${SIGNATURE}" \
  -H "X-Idempotency-Key: $(uuidgen 2>/dev/null || echo "idem-test-001")" \
  -d "${BODY}"
```

**Response (`202 Accepted`):**
```json
{
  "amount_pesewas": 2500,
  "external_reference": "mtap_798ba7ce_1731182400123",
  "message": "Please authorize the prompt on your phone (Mock)",
  "status": "PENDING",
  "transaction_id": "798ba7ce-54e7-40c2-9020-0082987114b3"
}
```

---

### B. Self-Service Tag Onboarding

#### 1. Initiate Pairing (`POST /api/v1/tags/initiate-pairing`)
Generates a 6-digit cryptographic OTP, hashes it in Redis (300s TTL), and dispatches via Africa's Talking.
```bash
curl -i -X POST https://m-tap-backend.onrender.com/api/v1/tags/initiate-pairing \
  -H "Content-Type: application/json" \
  -d '{
    "tag_uid": "04A38CF2196881",
    "phone_number": "233241234567"
  }'
```

**Response (`200 OK`):**
```json
{
  "message": "OTP activation code dispatched via SMS. Valid for 5 minutes.",
  "status": "PENDING_ACTIVATION",
  "tag_uid": "04A38CF2196881"
}
```

#### 2. Verify OTP (`POST /api/v1/tags/verify-otp`)
Validates the OTP, transitions tag to `ACTIVE`, and warms Redis `tag:active:{tag_uid}` for 24h.
```bash
curl -i -X POST https://m-tap-backend.onrender.com/api/v1/tags/verify-otp \
  -H "Content-Type: application/json" \
  -d '{
    "tag_uid": "04A38CF2196881",
    "otp": "123456"
  }'
```

#### 3. Emergency Tag Revocation (`POST /api/v1/tags/revoke`)
Immediately revokes tag in PostgreSQL, evicts from Redis cache, and writes to audit logs.
```bash
curl -i -X POST https://m-tap-backend.onrender.com/api/v1/tags/revoke \
  -H "Content-Type: application/json" \
  -d '{
    "tag_uid": "04A38CF2196881",
    "reason": "Customer reported lost card"
  }'
```

---

### C. Paystack Asynchronous Webhook (`POST /api/v1/payments/paystack/webhook`)
Pushes incoming webhook payload to Asynq `payments` queue within 30ms.

```bash
WEBHOOK_BODY='{"event":"charge.success","data":{"reference":"mtap_798ba7ce_1731182400123","status":"success","amount":2500}}'
SIG=$(echo -n "${WEBHOOK_BODY}" | openssl dgst -sha512 -hmac "sk_test_mock_secret_key" | awk '{print $2}')

curl -i -X POST https://m-tap-backend.onrender.com/api/v1/payments/paystack/webhook \
  -H "Content-Type: application/json" \
  -H "x-paystack-signature: ${SIG}" \
  -d "${WEBHOOK_BODY}"
```

**Response (`200 OK` in <30ms):**
```json
{
  "message": "Webhook acknowledged and dispatched to settlement engine",
  "status": true
}
```

### D. High-Throughput Benchmarking Simulator (`cmd/simulator/main.go`)

To stress-test proximity switching, rate limits, and HMAC signature throughput under concurrent load:
```bash
go run cmd/simulator/main.go -n 50 -c 5 -terminal POD_001 -tag 04A38CF2196880
```

Outputs live execution metrics:
- Average switching latency (ms)
- Throughput (Requests per second)
- Accepted taps (202 Accepted)
- Throttled taps (429 Too Many Requests per-terminal guard)
- Zero undetected duplicate charges

---

## 7. Testing & Verification

Run the full test suite with race detector:
```bash
go test -v -race ./...
```

---

## 8. Merchant Settlement & Payout Architectures

For an in-depth architectural breakdown of how merchants receive their funds, consult the comprehensive settlement guide:
- **[Merchant Payout & Settlement Architectures](docs/merchant_payout_approaches.md)**: Covers Nightly Batch Aggregation via Paystack Transfers API, Native Split Payments via Subaccounts, and On-Demand Self-Service Cash Out via USSD/POS, along with double-entry ledger tracking, fee economics, and Bank of Ghana (Act 987) compliance.


