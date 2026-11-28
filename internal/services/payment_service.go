package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/boronlabs/m-tap-backend/internal/config"
	"github.com/boronlabs/m-tap-backend/internal/middleware"
	"github.com/boronlabs/m-tap-backend/internal/models"
	"github.com/boronlabs/m-tap-backend/internal/tasks"
)

type PaymentService interface {
	ProcessTap(ctx context.Context, terminalID uuid.UUID, terminalIdentifier string, tagUID string, amountPesewas int64) (*TapResult, error)
	SubmitTapOTP(ctx context.Context, terminalID uuid.UUID, terminalIdentifier, reference, otp string) (*TapResult, error)
}

type TapResult struct {
	TransactionID     uuid.UUID `json:"transaction_id"`
	ExternalReference string    `json:"external_reference"`
	Status            string    `json:"status"`
	NextAction        string    `json:"next_action,omitempty"`
	AmountPesewas     int64     `json:"amount_pesewas"`
	Message           string    `json:"message"`
}

type paymentService struct {
	cfg         *config.Config
	db          *pgxpool.Pool
	rdb         *redis.Client
	tagService  TagService
	paystack    PaystackClient
	audit       AuditService
	asynqClient *asynq.Client
}

func NewPaymentService(
	cfg *config.Config,
	db *pgxpool.Pool,
	rdb *redis.Client,
	tagService TagService,
	paystack PaystackClient,
	audit AuditService,
	asynqClient *asynq.Client,
) PaymentService {
	return &paymentService{
		cfg:         cfg,
		db:          db,
		rdb:         rdb,
		tagService:  tagService,
		paystack:    paystack,
		audit:       audit,
		asynqClient: asynqClient,
	}
}

func (s *paymentService) ProcessTap(
	ctx context.Context,
	terminalID uuid.UUID,
	terminalIdentifier string,
	tagUID string,
	amountPesewas int64,
) (*TapResult, error) {
	if amountPesewas <= 0 {
		return nil, errors.New("amount must be a positive integer in minor units (pesewas)")
	}

	// 1. Resolve phone number and verify tag is ACTIVE (Sub-2ms cache with DB fallback)
	phoneNumber, err := s.tagService.ResolveActiveTagPhone(ctx, tagUID)
	if err != nil {
		// Record failed tap velocity against tag
		frozen, vErr := middleware.TrackTagVelocity(ctx, s.rdb, s.db, tagUID, terminalIdentifier, s.cfg.TagVelocityLimit)
		if vErr == nil && frozen {
			return nil, fmt.Errorf("tag velocity limit breached: tag %s has been frozen", tagUID)
		}
		return nil, fmt.Errorf("tag validation failed: %w", err)
	}

	// 2. Customer Daily Limit Check
	var dailyLimit int64
	var isBlocked bool
	custQuery := `
		SELECT daily_limit_pesewas, is_blocked
		FROM customers
		WHERE phone_number = $1
	`
	err = s.db.QueryRow(ctx, custQuery, phoneNumber).Scan(&dailyLimit, &isBlocked)
	if err != nil {
		return nil, fmt.Errorf("failed to verify customer account status: %w", err)
	}

	if isBlocked {
		return nil, errors.New("customer account is blocked by risk control")
	}

	// Calculate spent today
	var spentToday int64
	spendingQuery := `
		SELECT COALESCE(SUM(amount_pesewas), 0)
		FROM transactions
		WHERE tag_uid = $1
		  AND settlement_state = 'SETTLED'
		  AND created_at >= date_trunc('day', NOW())
	`
	_ = s.db.QueryRow(ctx, spendingQuery, tagUID).Scan(&spentToday)

	if spentToday+amountPesewas > dailyLimit {
		return nil, fmt.Errorf("daily spending limit of %d pesewas exceeded (spent today: %d pesewas, tap amount: %d pesewas)",
			dailyLimit, spentToday, amountPesewas)
	}

	// 3. Insert transaction record in PostgreSQL with status PENDING
	txnID := uuid.New()
	externalRef := fmt.Sprintf("mtap_%s_%d", txnID.String()[:8], time.Now().UnixNano()/1000)

	insertTxnQuery := `
		INSERT INTO transactions (id, terminal_id, tag_uid, amount_pesewas, external_reference, settlement_state, created_at)
		VALUES ($1, $2, $3, $4, $5, $6, NOW())
	`
	_, err = s.db.Exec(ctx, insertTxnQuery, txnID, terminalID, tagUID, amountPesewas, externalRef, models.SettlementPending)
	if err != nil {
		return nil, fmt.Errorf("failed to create pending transaction record: %w", err)
	}

	// 4. Trigger Paystack MoMo direct charge
	telcoProvider := GhanaTelcoProvider(phoneNumber)
	chargeResp, err := s.paystack.ChargeMobileMoney(ctx, ChargeRequest{
		AmountPesewas: amountPesewas,
		PhoneNumber:   phoneNumber,
		Reference:     externalRef,
		Provider:      telcoProvider,
	})
	if err != nil {
		slog.Error("Paystack Mobile Money charge dispatch failed", "error", err, "reference", externalRef)
		// Update txn state to DECLINED
		_, _ = s.db.Exec(ctx, "UPDATE transactions SET settlement_state = 'DECLINED' WHERE id = $1", txnID)
		_ = s.audit.Log(ctx, models.EntityTransaction, txnID.String(), models.ActionPaymentDeclined, models.ActorSystemRule, &terminalIdentifier, map[string]interface{}{
			"error": err.Error(),
		})
		return nil, fmt.Errorf("payment rail charge rejected: %w", err)
	}

	// 5. Append-only audit record
	_ = s.audit.Log(ctx, models.EntityTransaction, txnID.String(), models.ActionPaymentInitiated, models.ActorTerminal, &terminalIdentifier, map[string]interface{}{
		"tag_uid":            tagUID,
		"phone_number":       phoneNumber,
		"amount_pesewas":     amountPesewas,
		"external_reference": externalRef,
		"provider":           telcoProvider,
	})

	// 6. If MOCK_PAYSTACK_SUCCESS is true, simulate incoming clearing webhook via Asynq
	if s.cfg.MockPaystackSuccess {
		go func(ref string, amt int64) {
			time.Sleep(200 * time.Millisecond) // realistic micro-delay
			mockWebhookData, _ := json.Marshal(map[string]interface{}{
				"reference":       ref,
				"amount":          amt,
				"status":          "success",
				"gateway_response": "Approved (Mock MoMo Clearing)",
			})
			task, err := tasks.NewPaystackWebhookTask("charge.success", mockWebhookData)
			if err == nil {
				_, _ = s.asynqClient.Enqueue(task)
			}
		}(externalRef, amountPesewas)
	}

	return &TapResult{
		TransactionID:     txnID,
		ExternalReference: externalRef,
		Status:            models.SettlementPending,
		NextAction:        chargeResp.PaystackStatus,
		AmountPesewas:     amountPesewas,
		Message:           chargeResp.DisplayText,
	}, nil
}

func (s *paymentService) SubmitTapOTP(
	ctx context.Context,
	terminalID uuid.UUID,
	terminalIdentifier string,
	reference string,
	otp string,
) (*TapResult, error) {
	if reference == "" {
		return nil, errors.New("reference is required")
	}
	validOTP, err := ValidateOTPCode(otp)
	if err != nil {
		return nil, err
	}

	var txnID uuid.UUID
	var amountPesewas int64
	var state string
	query := `
		SELECT id, amount_pesewas, settlement_state
		FROM transactions
		WHERE external_reference = $1 AND terminal_id = $2
	`
	if err := s.db.QueryRow(ctx, query, reference, terminalID).Scan(&txnID, &amountPesewas, &state); err != nil {
		return nil, fmt.Errorf("transaction not found for this terminal: %w", err)
	}
	if state != models.SettlementPending {
		return nil, fmt.Errorf("transaction is already '%s' and cannot accept OTP", state)
	}

	resp, err := s.paystack.SubmitOTP(ctx, reference, validOTP)
	if err != nil {
		return nil, fmt.Errorf("payment rail OTP submission failed: %w", err)
	}

	_ = s.audit.Log(ctx, models.EntityTransaction, txnID.String(), models.ActionPaymentInitiated, models.ActorTerminal, &terminalIdentifier, map[string]interface{}{
		"external_reference": reference,
		"paystack_status":    resp.PaystackStatus,
		"step":               "submit_otp",
	})

	return &TapResult{
		TransactionID:     txnID,
		ExternalReference: reference,
		Status:            state,
		NextAction:        resp.PaystackStatus,
		AmountPesewas:     amountPesewas,
		Message:           resp.DisplayText,
	}, nil
}

