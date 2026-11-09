package models

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
)

// Status constants
const (
	StatusActive            = "ACTIVE"
	StatusInactive          = "INACTIVE"
	StatusPendingActivation = "PENDING_ACTIVATION"
	StatusFrozen            = "FROZEN"
	StatusRevoked           = "REVOKED"

	SettlementPending  = "PENDING"
	SettlementSettled  = "SETTLED"
	SettlementDeclined = "DECLINED"
	SettlementTimeout  = "TIMEOUT"

	ActorCustomer   = "CUSTOMER"
	ActorMerchant   = "MERCHANT"
	ActorTerminal   = "TERMINAL"
	ActorSystemRule = "SYSTEM_RULE"
	ActorAdmin      = "ADMIN"

	EntityCustomerTag = "CUSTOMER_TAG"
	EntityTerminal    = "TERMINAL"
	EntityTransaction = "TRANSACTION"

	ActionTagPairInitiated = "TAG_PAIR_INITIATED"
	ActionTagActivated     = "TAG_ACTIVATED"
	ActionTagFrozen        = "TAG_FROZEN"
	ActionTagRevoked       = "TAG_REVOKED"
	ActionAuthFailed       = "AUTH_FAILED"
	ActionPaymentInitiated = "PAYMENT_INITIATED"
	ActionPaymentSettled   = "PAYMENT_SETTLED"
	ActionPaymentDeclined  = "PAYMENT_DECLINED"
)

// Merchant represents a micro-retailer merchant.
type Merchant struct {
	ID               uuid.UUID `json:"id"`
	MerchantCode     string    `json:"merchant_code"`
	BusinessName     string    `json:"business_name"`
	SettlementMSISDN string    `json:"settlement_msisdn"`
	Status           string    `json:"status"`
	CreatedAt        time.Time `json:"created_at"`
}

// Terminal represents a hardware NFC POS terminal deployed at a merchant stall.
type Terminal struct {
	ID                 uuid.UUID  `json:"id"`
	MerchantID         uuid.UUID  `json:"merchant_id"`
	TerminalIdentifier string     `json:"terminal_identifier"`
	HMACSecret         string     `json:"-"` // Never expose PSK over JSON
	Status             string     `json:"status"`
	LastHeartbeat      *time.Time `json:"last_heartbeat,omitempty"`
	CreatedAt          time.Time  `json:"created_at"`
}

// Customer represents a consumer holding Mobile Money wallets and registered NFC tags.
type Customer struct {
	ID                uuid.UUID `json:"id"`
	PhoneNumber       string    `json:"phone_number"`        // E.164: 233XXXXXXXXX
	DailyLimitPesewas int64     `json:"daily_limit_pesewas"` // 50000 = GHS 500.00
	IsBlocked         bool      `json:"is_blocked"`
	CreatedAt         time.Time `json:"created_at"`
}

// CustomerTag represents the physical 7-byte UID NFC tag.
type CustomerTag struct {
	TagUID            string     `json:"tag_uid"` // e.g. 04A38CF2196880
	CustomerID        *uuid.UUID `json:"customer_id,omitempty"`
	Status            string     `json:"status"`
	FailedPromptCount int        `json:"failed_prompt_count"`
	ActivatedAt       *time.Time `json:"activated_at,omitempty"`
	UpdatedAt         time.Time  `json:"updated_at"`
}

// Transaction represents a proximity micro-retail tap transaction.
// All amounts are strictly in minor units (Pesewas) with zero floating-point math.
type Transaction struct {
	ID                uuid.UUID  `json:"id"`
	TerminalID        uuid.UUID  `json:"terminal_id"`
	TagUID            string     `json:"tag_uid"`
	AmountPesewas     int64      `json:"amount_pesewas"`
	ExternalReference string     `json:"external_reference"`
	SettlementState   string     `json:"settlement_state"`
	LatencyMs         *int       `json:"latency_ms,omitempty"`
	CreatedAt         time.Time  `json:"created_at"`
	FinalizedAt       *time.Time `json:"finalized_at,omitempty"`
}

// AuditLog is an append-only forensic compliance and audit ledger entry.
type AuditLog struct {
	ID         uuid.UUID       `json:"id"`
	EntityType string          `json:"entity_type"`
	EntityID   string          `json:"entity_id"`
	Action     string          `json:"action"`
	ActorType  string          `json:"actor_type"`
	ActorID    *string         `json:"actor_id,omitempty"`
	Metadata   json.RawMessage `json:"metadata"`
	CreatedAt  time.Time       `json:"created_at"`
}
