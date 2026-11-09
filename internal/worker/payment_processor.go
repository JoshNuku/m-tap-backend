package worker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/boronlabs/m-tap-backend/internal/middleware"
	"github.com/boronlabs/m-tap-backend/internal/models"
	"github.com/boronlabs/m-tap-backend/internal/services"
	"github.com/boronlabs/m-tap-backend/internal/tasks"
)

type PaymentWebhookProcessor struct {
	db    *pgxpool.Pool
	rdb   *redis.Client
	audit services.AuditService
}

func NewPaymentWebhookProcessor(db *pgxpool.Pool, rdb *redis.Client, audit services.AuditService) *PaymentWebhookProcessor {
	return &PaymentWebhookProcessor{
		db:    db,
		rdb:   rdb,
		audit: audit,
	}
}

type paystackData struct {
	Reference string `json:"reference"`
	Status    string `json:"status"`
	Amount    int64  `json:"amount"`
	Message   string `json:"message"`
}

func (p *PaymentWebhookProcessor) ProcessTask(ctx context.Context, t *asynq.Task) error {
	var payload tasks.PaystackWebhookPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return fmt.Errorf("failed to unmarshal webhook task payload: %w", err)
	}

	var data paystackData
	if err := json.Unmarshal(payload.RawData, &data); err != nil {
		return fmt.Errorf("failed to extract paystack data: %w", err)
	}

	if data.Reference == "" {
		slog.Warn("Paystack webhook received with empty reference, skipping", "event", payload.Event)
		return nil
	}

	slog.Info("Processing Paystack payment settlement task",
		"reference", data.Reference,
		"event", payload.Event,
		"status", data.Status,
	)

	// Execute DB settlement transaction with FOR UPDATE row lock
	tx, err := p.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to begin db transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	var txnID uuid.UUID
	var currentState string
	var createdAt time.Time
	var tagUID string
	var amountPesewas int64

	queryLock := `
		SELECT id, settlement_state, created_at, tag_uid, amount_pesewas
		FROM transactions
		WHERE external_reference = $1
		FOR UPDATE
	`
	err = tx.QueryRow(ctx, queryLock, data.Reference).Scan(
		&txnID,
		&currentState,
		&createdAt,
		&tagUID,
		&amountPesewas,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			slog.Warn("Transaction not found for reference", "reference", data.Reference)
			return nil // Transaction may belong to other system, exit idempotently
		}
		return fmt.Errorf("failed to lock transaction row: %w", err)
	}

	// Idempotency: If already settled, exit immediately
	if currentState == models.SettlementSettled {
		slog.Info("Transaction already settled, exiting idempotently", "reference", data.Reference)
		return nil
	}

	now := time.Now()
	latencyMs := int(now.Sub(createdAt).Milliseconds())

	// Evaluate charge outcome
	if payload.Event == "charge.success" || data.Status == "success" {
		updateQuery := `
			UPDATE transactions
			SET settlement_state = $1, latency_ms = $2, finalized_at = $3
			WHERE id = $4
		`
		if _, err := tx.Exec(ctx, updateQuery, models.SettlementSettled, latencyMs, now, txnID); err != nil {
			return fmt.Errorf("failed to update settled transaction: %w", err)
		}

		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("failed to commit settlement: %w", err)
		}

		// Reset tag velocity counter upon successful tap
		middleware.ResetTagVelocity(ctx, p.rdb, tagUID)

		// Record append-only audit
		_ = p.audit.Log(ctx, models.EntityTransaction, txnID.String(), models.ActionPaymentSettled, models.ActorSystemRule, nil, map[string]interface{}{
			"external_reference": data.Reference,
			"settlement_state":   models.SettlementSettled,
			"latency_ms":         latencyMs,
			"amount_pesewas":     amountPesewas,
			"cleared_at":         now,
		})

		slog.Info("TRANSACTION SETTLED SUCCESSFULLY",
			"txn_id", txnID,
			"reference", data.Reference,
			"amount_pesewas", amountPesewas,
			"latency_ms", latencyMs,
		)
	} else {
		// Payment failed or declined
		updateQuery := `
			UPDATE transactions
			SET settlement_state = $1, latency_ms = $2, finalized_at = $3
			WHERE id = $4
		`
		if _, err := tx.Exec(ctx, updateQuery, models.SettlementDeclined, latencyMs, now, txnID); err != nil {
			return fmt.Errorf("failed to update declined transaction: %w", err)
		}

		if err := tx.Commit(ctx); err != nil {
			return fmt.Errorf("failed to commit decline: %w", err)
		}

		// Track failed tap velocity
		_, _ = middleware.TrackTagVelocity(ctx, p.rdb, p.db, tagUID, "PAYSTACK_WEBHOOK")

		_ = p.audit.Log(ctx, models.EntityTransaction, txnID.String(), models.ActionPaymentDeclined, models.ActorSystemRule, nil, map[string]interface{}{
			"external_reference": data.Reference,
			"settlement_state":   models.SettlementDeclined,
			"gateway_message":    data.Message,
		})

		slog.Warn("Transaction settlement declined",
			"txn_id", txnID,
			"reference", data.Reference,
			"message", data.Message,
		)
	}

	return nil
}

