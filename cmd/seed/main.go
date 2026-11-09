package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log/slog"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/boronlabs/m-tap-backend/internal/config"
	"github.com/boronlabs/m-tap-backend/internal/db"
	"github.com/boronlabs/m-tap-backend/internal/models"
	mtapredis "github.com/boronlabs/m-tap-backend/internal/redis"
)

func main() {
	cfg := config.Load()
	ctx := context.Background()

	slog.Info("Starting database seed script for M-Tap local sandbox")

	dbPool, err := db.InitPool(ctx, cfg)
	if err != nil {
		slog.Error("Database connection failed", "error", err)
		os.Exit(1)
	}
	defer dbPool.Close()

	if err := db.RunMigrations(ctx, dbPool); err != nil {
		slog.Error("Migration failed", "error", err)
		os.Exit(1)
	}

	rdb, err := mtapredis.InitClient(ctx, cfg)
	if err != nil {
		slog.Error("Redis connection failed", "error", err)
		os.Exit(1)
	}
	defer rdb.Close()

	// 1. Seed Merchant
	var merchantID uuid.UUID
	merchantQuery := `
		INSERT INTO merchants (merchant_code, business_name, settlement_msisdn, status)
		VALUES ('MERCH_BORON_01', 'Boron Micro-Mart Accra Central', '233240000001', 'ACTIVE')
		ON CONFLICT (merchant_code) DO UPDATE
		SET business_name = EXCLUDED.business_name
		RETURNING id;
	`
	if err := dbPool.QueryRow(ctx, merchantQuery).Scan(&merchantID); err != nil {
		slog.Error("Failed to seed merchant", "error", err)
		os.Exit(1)
	}
	slog.Info("Merchant seeded", "merchant_code", "MERCH_BORON_01", "id", merchantID)

	// 2. Seed Terminal
	terminalIdentifier := "POD_001"
	terminalPSK := "secret_hmac_psk_key_boron_labs_2026"
	var terminalID uuid.UUID
	terminalQuery := `
		INSERT INTO terminals (merchant_id, terminal_identifier, hmac_secret, status)
		VALUES ($1, $2, $3, 'ACTIVE')
		ON CONFLICT (terminal_identifier) DO UPDATE
		SET hmac_secret = EXCLUDED.hmac_secret
		RETURNING id;
	`
	if err := dbPool.QueryRow(ctx, terminalQuery, merchantID, terminalIdentifier, terminalPSK).Scan(&terminalID); err != nil {
		slog.Error("Failed to seed terminal", "error", err)
		os.Exit(1)
	}
	slog.Info("Terminal seeded", "terminal_identifier", terminalIdentifier, "psk", terminalPSK)

	// 3. Seed Customer
	customerPhone := "233241234567"
	var customerID uuid.UUID
	customerQuery := `
		INSERT INTO customers (phone_number, daily_limit_pesewas, is_blocked)
		VALUES ($1, 50000, FALSE)
		ON CONFLICT (phone_number) DO UPDATE
		SET is_blocked = FALSE
		RETURNING id;
	`
	if err := dbPool.QueryRow(ctx, customerQuery, customerPhone).Scan(&customerID); err != nil {
		slog.Error("Failed to seed customer", "error", err)
		os.Exit(1)
	}
	slog.Info("Customer seeded", "phone_number", customerPhone, "id", customerID)

	// 4. Seed Active NFC Tag
	activeTagUID := "04A38CF2196880"
	tagQuery := `
		INSERT INTO customer_tags (tag_uid, customer_id, status, activated_at, updated_at)
		VALUES ($1, $2, 'ACTIVE', NOW(), NOW())
		ON CONFLICT (tag_uid) DO UPDATE
		SET customer_id = EXCLUDED.customer_id, status = 'ACTIVE', updated_at = NOW();
	`
	if _, err := dbPool.Exec(ctx, tagQuery, activeTagUID, customerID); err != nil {
		slog.Error("Failed to seed active customer tag", "error", err)
		os.Exit(1)
	}

	// Warm Redis Cache
	_ = rdb.Set(ctx, fmt.Sprintf("tag:active:%s", activeTagUID), customerPhone, 24*time.Hour).Err()
	slog.Info("Active Tag seeded and cached", "tag_uid", activeTagUID, "phone", customerPhone)

	// 5. Seed Unpaired Tag
	pendingTagUID := "04A38CF2196881"
	pendingQuery := `
		INSERT INTO customer_tags (tag_uid, customer_id, status, updated_at)
		VALUES ($1, NULL, 'PENDING_ACTIVATION', NOW())
		ON CONFLICT (tag_uid) DO UPDATE
		SET status = 'PENDING_ACTIVATION';
	`
	_, _ = dbPool.Exec(ctx, pendingQuery, pendingTagUID)
	slog.Info("Unpaired Tag seeded", "tag_uid", pendingTagUID, "status", models.StatusPendingActivation)

	// 6. Generate and print ready-to-test curl command with dynamic HMAC
	ts := time.Now().Unix()
	body := fmt.Sprintf(`{"terminal_id":"%s","tag_uid":"%s","amount_pesewas":2500}`, terminalIdentifier, activeTagUID)
	mac := hmac.New(sha256.New, []byte(terminalPSK))
	mac.Write([]byte(fmt.Sprintf("%d.%s", ts, body)))
	sig := hex.EncodeToString(mac.Sum(nil))

	fmt.Println("\n=======================================================")
	fmt.Println("       M-TAP SEEDING COMPLETE - READY FOR BENCH TEST   ")
	fmt.Println("=======================================================")
	fmt.Println("\nSample Tap Command (valid for 30s):")
	fmt.Printf(`curl -X POST http://localhost:%s/api/v1/terminal/tap \
  -H "Content-Type: application/json" \
  -H "X-Terminal-Id: %s" \
  -H "X-Timestamp: %d" \
  -H "X-Signature: %s" \
  -H "X-Idempotency-Key: %s" \
  -d '%s'`+"\n\n", cfg.Port, terminalIdentifier, ts, sig, uuid.New().String(), body)
}

