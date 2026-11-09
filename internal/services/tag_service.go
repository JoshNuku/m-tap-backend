package services

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"regexp"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/boronlabs/m-tap-backend/internal/middleware"
	"github.com/boronlabs/m-tap-backend/internal/models"
	"github.com/boronlabs/m-tap-backend/internal/tasks"
)

var (
	tagHexRegex   = regexp.MustCompile(`^[0-9A-Fa-f]{14}$`)
	phoneRegex    = regexp.MustCompile(`^233[0-9]{9}$`)
	ErrInvalidTag = errors.New("tag_uid must be a valid 14-character (7-byte) hexadecimal string")
	ErrInvalidPhone = errors.New("phone_number must be a Ghanaian mobile number in E.164 format: 233XXXXXXXXX")
)

type TagService interface {
	InitiatePairing(ctx context.Context, tagUID, phoneNumber, clientIP string) error
	VerifyOTP(ctx context.Context, tagUID, otpInput string) error
	RevokeTag(ctx context.Context, tagUID, reason, actorType, actorID string) error
	ResolveActiveTagPhone(ctx context.Context, tagUID string) (string, error)
}

type tagService struct {
	db          *pgxpool.Pool
	rdb         *redis.Client
	asynqClient *asynq.Client
	audit       AuditService
}

func NewTagService(db *pgxpool.Pool, rdb *redis.Client, asynqClient *asynq.Client, audit AuditService) TagService {
	return &tagService{
		db:          db,
		rdb:         rdb,
		asynqClient: asynqClient,
		audit:       audit,
	}
}

type otpState struct {
	PhoneNumber string `json:"phone_number"`
	HashedOTP   string `json:"hashed_otp"`
}

func (s *tagService) InitiatePairing(ctx context.Context, tagUID, phoneNumber, clientIP string) error {
	tagUID = strings.ToUpper(strings.TrimSpace(tagUID))
	phoneNumber = strings.TrimSpace(phoneNumber)

	if !tagHexRegex.MatchString(tagUID) {
		return ErrInvalidTag
	}
	if !phoneRegex.MatchString(phoneNumber) {
		return ErrInvalidPhone
	}

	// 1. Enforce per-phone SMS rate limit (60s cooldown)
	if err := middleware.CheckPhoneSMSRateLimit(ctx, s.rdb, phoneNumber); err != nil {
		return err
	}

	// 2. Generate 6-digit cryptographically secure OTP
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return fmt.Errorf("failed to generate secure OTP: %w", err)
	}
	otpCode := fmt.Sprintf("%06d", n.Int64())

	// 3. Hash OTP with SHA-256
	h := sha256.Sum256([]byte(otpCode))
	hashedOTP := hex.EncodeToString(h[:])

	// 4. Store in Redis otp:{tag_uid} (300s TTL)
	otpPayload, err := json.Marshal(otpState{
		PhoneNumber: phoneNumber,
		HashedOTP:   hashedOTP,
	})
	if err != nil {
		return fmt.Errorf("failed to serialize OTP state: %w", err)
	}

	otpKey := fmt.Sprintf("otp:%s", tagUID)
	if err := s.rdb.Set(ctx, otpKey, string(otpPayload), 300*time.Second).Err(); err != nil {
		return fmt.Errorf("failed to persist OTP state in cache: %w", err)
	}

	// 5. Upsert customer and tag in PostgreSQL
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("failed to start transaction: %w", err)
	}
	defer tx.Rollback(ctx)

	var customerID uuid.UUID
	upsertCustomerQuery := `
		INSERT INTO customers (phone_number)
		VALUES ($1)
		ON CONFLICT (phone_number) DO UPDATE
		SET phone_number = EXCLUDED.phone_number
		RETURNING id
	`
	if err := tx.QueryRow(ctx, upsertCustomerQuery, phoneNumber).Scan(&customerID); err != nil {
		return fmt.Errorf("failed to upsert customer: %w", err)
	}

	upsertTagQuery := `
		INSERT INTO customer_tags (tag_uid, customer_id, status, updated_at)
		VALUES ($1, $2, $3, NOW())
		ON CONFLICT (tag_uid) DO UPDATE
		SET customer_id = EXCLUDED.customer_id,
		    status = EXCLUDED.status,
		    updated_at = NOW()
	`
	if _, err := tx.Exec(ctx, upsertTagQuery, tagUID, customerID, models.StatusPendingActivation); err != nil {
		return fmt.Errorf("failed to upsert customer tag: %w", err)
	}

	if err := tx.Commit(ctx); err != nil {
		return fmt.Errorf("failed to commit pairing initiation: %w", err)
	}

	// 6. Enqueue non-blocking SMS OTP dispatch task onto Asynq notifications queue
	task, err := tasks.NewSendOTPTask(tagUID, phoneNumber, otpCode)
	if err != nil {
		slog.Error("Failed to build Asynq OTP task", "error", err)
	} else if _, err := s.asynqClient.EnqueueContext(ctx, task); err != nil {
		slog.Error("Failed to enqueue Asynq OTP task", "error", err)
	}

	// 7. Write audit log
	_ = s.audit.Log(ctx, models.EntityCustomerTag, tagUID, models.ActionTagPairInitiated, models.ActorCustomer, &phoneNumber, map[string]interface{}{
		"ip":           clientIP,
		"phone_number": phoneNumber,
	})

	return nil
}

func (s *tagService) VerifyOTP(ctx context.Context, tagUID, otpInput string) error {
	tagUID = strings.ToUpper(strings.TrimSpace(tagUID))
	otpInput = strings.TrimSpace(otpInput)

	if !tagHexRegex.MatchString(tagUID) {
		return ErrInvalidTag
	}

	// 1. Validate OTP from Redis
	otpKey := fmt.Sprintf("otp:%s", tagUID)
	val, err := s.rdb.Get(ctx, otpKey).Result()
	if err != nil {
		if errors.Is(err, redis.Nil) {
			return errors.New("OTP expired or pairing was never initiated for this tag")
		}
		return fmt.Errorf("failed to query OTP: %w", err)
	}

	var state otpState
	if err := json.Unmarshal([]byte(val), &state); err != nil {
		return fmt.Errorf("failed to deserialize cached OTP: %w", err)
	}

	// 2. Hash input and verify
	h := sha256.Sum256([]byte(otpInput))
	inputHash := hex.EncodeToString(h[:])

	if inputHash != state.HashedOTP {
		return errors.New("invalid OTP code entered")
	}

	// 3. Update PostgreSQL: tag status to ACTIVE, activated_at = NOW()
	now := time.Now()
	query := `
		UPDATE customer_tags
		SET status = $1, activated_at = $2, failed_prompt_count = 0, updated_at = $3
		WHERE tag_uid = $4
	`
	cmdTag, err := s.db.Exec(ctx, query, models.StatusActive, now, now, tagUID)
	if err != nil {
		return fmt.Errorf("failed to activate customer tag in db: %w", err)
	}
	if cmdTag.RowsAffected() == 0 {
		return errors.New("tag record not found in database")
	}

	// 4. Set Redis tag:active:{tag_uid} -> phone_number (24 Hours TTL)
	activeKey := fmt.Sprintf("tag:active:%s", tagUID)
	if err := s.rdb.Set(ctx, activeKey, state.PhoneNumber, 24*time.Hour).Err(); err != nil {
		slog.Error("Failed to populate active tag cache", "error", err, "tag_uid", tagUID)
	}

	// 5. Delete OTP key
	_ = s.rdb.Del(ctx, otpKey).Err()

	// 6. Write audit log
	_ = s.audit.Log(ctx, models.EntityCustomerTag, tagUID, models.ActionTagActivated, models.ActorCustomer, &state.PhoneNumber, map[string]interface{}{
		"phone_number": state.PhoneNumber,
		"activated_at": now,
	})

	return nil
}

func (s *tagService) RevokeTag(ctx context.Context, tagUID, reason, actorType, actorID string) error {
	tagUID = strings.ToUpper(strings.TrimSpace(tagUID))

	// 1. Update status to REVOKED in PostgreSQL
	query := `
		UPDATE customer_tags
		SET status = $1, updated_at = NOW()
		WHERE tag_uid = $2
	`
	res, err := s.db.Exec(ctx, query, models.StatusRevoked, tagUID)
	if err != nil {
		return fmt.Errorf("failed to revoke tag in database: %w", err)
	}
	if res.RowsAffected() == 0 {
		return errors.New("tag not found")
	}

	// 2. Evict cache immediately
	activeKey := fmt.Sprintf("tag:active:%s", tagUID)
	_ = s.rdb.Del(ctx, activeKey).Err()

	// 3. Write audit log
	_ = s.audit.Log(ctx, models.EntityCustomerTag, tagUID, models.ActionTagRevoked, actorType, &actorID, map[string]interface{}{
		"reason": reason,
	})

	return nil
}

func (s *tagService) ResolveActiveTagPhone(ctx context.Context, tagUID string) (string, error) {
	tagUID = strings.ToUpper(strings.TrimSpace(tagUID))

	// 1. Read from Redis tag:active:{tag_uid} (sub-2ms cache)
	activeKey := fmt.Sprintf("tag:active:%s", tagUID)
	cachedPhone, err := s.rdb.Get(ctx, activeKey).Result()
	if err == nil && cachedPhone != "" {
		return cachedPhone, nil
	}

	// 2. Cache miss: Fallback to PostgreSQL
	query := `
		SELECT c.phone_number, ct.status
		FROM customer_tags ct
		JOIN customers c ON ct.customer_id = c.id
		WHERE ct.tag_uid = $1
	`
	var phoneNumber, status string
	err = s.db.QueryRow(ctx, query, tagUID).Scan(&phoneNumber, &status)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", errors.New("tag is unregistered")
		}
		return "", fmt.Errorf("db lookup failed: %w", err)
	}

	if status != models.StatusActive {
		return "", fmt.Errorf("tag status is '%s', cannot authorize taps", status)
	}

	// Populate cache for 24h
	_ = s.rdb.Set(ctx, activeKey, phoneNumber, 24*time.Hour).Err()

	return phoneNumber, nil
}

