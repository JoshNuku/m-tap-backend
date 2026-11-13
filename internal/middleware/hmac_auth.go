package middleware

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"

	"github.com/boronlabs/m-tap-backend/internal/config"
	"github.com/boronlabs/m-tap-backend/internal/models"
	"github.com/gin-gonic/gin"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	HeaderTerminalID = "X-Terminal-Id"
	HeaderTimestamp  = "X-Timestamp"
	HeaderSignature  = "X-Signature"

	ContextTerminalKey = "authenticated_terminal"
)

// TerminalHMACAuth validates terminal identity, anti-replay window, and HMAC-SHA256 signature.
func TerminalHMACAuth(db *pgxpool.Pool, cfg *config.Config) gin.HandlerFunc {
	replayWindow := cfg.ReplayWindowSec
	if replayWindow <= 0 {
		replayWindow = 30
	}

	return func(c *gin.Context) {
		terminalID := c.GetHeader(HeaderTerminalID)
		timestampStr := c.GetHeader(HeaderTimestamp)
		providedSignature := c.GetHeader(HeaderSignature)

		if terminalID == "" || timestampStr == "" || providedSignature == "" {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error":   "MISSING_AUTH_HEADERS",
				"message": "Required headers: X-Terminal-Id, X-Timestamp, X-Signature",
			})
			return
		}

		// 1. Anti-Replay: Verify |now - timestamp| <= replayWindow
		timestampUnix, err := strconv.ParseInt(timestampStr, 10, 64)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error":   "INVALID_TIMESTAMP_FORMAT",
				"message": "X-Timestamp must be a valid Unix epoch seconds integer",
			})
			return
		}

		nowUnix := time.Now().Unix()
		diff := math.Abs(float64(nowUnix - timestampUnix))
		if diff > float64(replayWindow) {
			slog.Warn("Terminal request rejected: timestamp drift exceeds replay window",
				"terminal_id", terminalID,
				"client_ts", timestampUnix,
				"server_ts", nowUnix,
				"drift_sec", diff,
				"allowed_sec", replayWindow,
			)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error":   "REPLAY_ATTACK_DETECTED",
				"message": fmt.Sprintf("Timestamp drift %.1fs exceeds maximum allowance of %ds (server time: %d)", diff, replayWindow, nowUnix),
			})
			return
		}

		// 2. Read and cache raw request body
		bodyBytes, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"error":   "FAILED_TO_READ_BODY",
				"message": "Unable to read request payload",
			})
			return
		}
		// Reset body so downstream handlers can bind JSON
		c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

		// 3. Retrieve terminal & pre-shared secret from DB
		var terminal models.Terminal
		query := `
			SELECT id, merchant_id, terminal_identifier, hmac_secret, status
			FROM terminals
			WHERE terminal_identifier = $1 AND status = 'ACTIVE'
		`
		err = db.QueryRow(c.Request.Context(), query, terminalID).Scan(
			&terminal.ID,
			&terminal.MerchantID,
			&terminal.TerminalIdentifier,
			&terminal.HMACSecret,
			&terminal.Status,
		)
		if err != nil {
			if err == pgx.ErrNoRows {
				slog.Warn("Terminal not found or inactive", "terminal_id", terminalID)
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
					"error":   "INVALID_TERMINAL",
					"message": "Terminal is unrecognized, revoked, or inactive",
				})
				return
			}
			slog.Error("Failed to query terminal secret", "error", err, "terminal_id", terminalID)
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{
				"error":   "DATABASE_ERROR",
				"message": "Failed to verify terminal credentials",
			})
			return
		}

		// 4. Compute HMAC-SHA256(PreSharedKey, timestamp + "." + raw_body)
		mac := hmac.New(sha256.New, []byte(terminal.HMACSecret))
		mac.Write([]byte(timestampStr))
		mac.Write([]byte("."))
		mac.Write(bodyBytes)
		computedHash := mac.Sum(nil)
		computedHex := hex.EncodeToString(computedHash)

		// 5. Constant-time comparison
		if !hmac.Equal([]byte(computedHex), []byte(providedSignature)) {
			slog.Warn("Terminal HMAC signature verification failed",
				"terminal_id", terminalID,
				"expected", computedHex,
				"provided", providedSignature,
			)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error":   "INVALID_SIGNATURE",
				"message": "HMAC-SHA256 signature verification failed",
			})
			return
		}

		// Update terminal last_heartbeat asynchronously
		go func(tID string) {
			_, _ = db.Exec(context.Background(),
				"UPDATE terminals SET last_heartbeat = NOW() WHERE terminal_identifier = $1", tID)
		}(terminalID)

		// Store authenticated terminal in context
		c.Set(ContextTerminalKey, terminal)
		c.Next()
	}
}
