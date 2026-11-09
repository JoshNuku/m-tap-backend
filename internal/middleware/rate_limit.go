package middleware

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/redis/go-redis/v9"
	"github.com/boronlabs/m-tap-backend/internal/models"
)

// TerminalRateLimit enforces max 1 tap per 3 seconds per terminal_id.
func TerminalRateLimit(rdb *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		terminalID := c.GetHeader(HeaderTerminalID)
		if terminalID == "" {
			c.Next()
			return
		}

		key := fmt.Sprintf("ratelimit:terminal:%s", terminalID)
		// Atomic SET NX EX 3s
		allowed, err := rdb.SetNX(c.Request.Context(), key, "1", 3*time.Second).Result()
		if err != nil {
			slog.Error("Terminal rate limit check error", "error", err)
			c.Next()
			return
		}

		if !allowed {
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
				"error":   "TERMINAL_RATE_LIMIT_EXCEEDED",
				"message": "Maximum 1 tap per 3 seconds allowed per terminal.",
			})
			return
		}

		c.Next()
	}
}

// SMSRateLimit enforces max 1 OTP SMS per 60s per phone number and max 5 OTPs per IP per hour.
func SMSRateLimit(rdb *redis.Client) gin.HandlerFunc {
	return func(c *gin.Context) {
		clientIP := c.ClientIP()

		// 1. Check IP rate limit (max 5 per 3600s)
		ipKey := fmt.Sprintf("ratelimit:sms:ip:%s", clientIP)
		ipAttempts, err := rdb.Incr(c.Request.Context(), ipKey).Result()
		if err == nil {
			if ipAttempts == 1 {
				_ = rdb.Expire(c.Request.Context(), ipKey, 1*time.Hour).Err()
			}
			if ipAttempts > 5 {
				c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{
					"error":   "IP_RATE_LIMIT_EXCEEDED",
					"message": "Too many OTP requests from this IP address. Please try again in an hour.",
				})
				return
			}
		}

		c.Next()
	}
}

// CheckPhoneSMSRateLimit checks the 60-second limit for a specific phone number.
func CheckPhoneSMSRateLimit(ctx context.Context, rdb *redis.Client, phoneNumber string) error {
	phoneKey := fmt.Sprintf("ratelimit:sms:phone:%s", phoneNumber)
	allowed, err := rdb.SetNX(ctx, phoneKey, "1", 60*time.Second).Result()
	if err != nil {
		return fmt.Errorf("failed to check phone rate limit: %w", err)
	}
	if !allowed {
		return fmt.Errorf("please wait 60 seconds before requesting another activation code")
	}
	return nil
}

// TrackTagVelocity records consecutive failed/unconfirmed attempts.
// If threshold (3 failed taps in 10 minutes) is breached, automatically freezes the tag.
func TrackTagVelocity(ctx context.Context, rdb *redis.Client, db *pgxpool.Pool, tagUID string, actorID string) (bool, error) {
	key := fmt.Sprintf("velocity:tag:%s", tagUID)
	count, err := rdb.Incr(ctx, key).Result()
	if err != nil {
		return false, fmt.Errorf("failed to increment tag velocity: %w", err)
	}

	if count == 1 {
		_ = rdb.Expire(ctx, key, 600*time.Second).Err() // 10 minutes sliding window
	}

	if count >= 3 {
		slog.Warn("SECURITY ALERT: Tag velocity limit breached. Auto-freezing tag.",
			"tag_uid", tagUID,
			"failed_attempts", count,
		)

		// 1. Update status to FROZEN in PostgreSQL
		_, updateErr := db.Exec(ctx, `
			UPDATE customer_tags 
			SET status = $1, failed_prompt_count = failed_prompt_count + 1, updated_at = NOW()
			WHERE tag_uid = $2
		`, models.StatusFrozen, tagUID)
		if updateErr != nil {
			slog.Error("Failed to freeze tag in database", "error", updateErr, "tag_uid", tagUID)
		}

		// 2. Evict tag:active:{tag_uid} from Redis
		_ = rdb.Del(ctx, fmt.Sprintf("tag:active:%s", tagUID)).Err()

		// 3. Insert audit log
		meta := fmt.Sprintf(`{"breach_count": %d, "reason": "3_failed_taps_in_10m"}`, count)
		_, _ = db.Exec(ctx, `
			INSERT INTO audit_logs (entity_type, entity_id, action, actor_type, actor_id, metadata, created_at)
			VALUES ($1, $2, $3, $4, $5, $6::jsonb, NOW())
		`, models.EntityCustomerTag, tagUID, models.ActionTagFrozen, models.ActorSystemRule, actorID, meta)

		return true, nil // Tag is now frozen
	}

	return false, nil
}

// ResetTagVelocity clears the velocity counter upon a successfully settled tap.
func ResetTagVelocity(ctx context.Context, rdb *redis.Client, tagUID string) {
	_ = rdb.Del(ctx, fmt.Sprintf("velocity:tag:%s", tagUID)).Err()
}

