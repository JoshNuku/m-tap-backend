package middleware

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/boronlabs/m-tap-backend/internal/config"
)

var allowedPaystackIPs = map[string]struct{}{
	"52.31.139.75":  {},
	"52.49.173.169": {},
	"52.214.14.220": {},
}

// PaystackSecurityDefense verifies IP whitelisting and HMAC-SHA512 raw body payload signature.
func PaystackSecurityDefense(cfg *config.Config) gin.HandlerFunc {
	return func(c *gin.Context) {
		clientIP := c.ClientIP()

		// 1. IP Whitelist check (can be bypassed in dev/testing via config)
		if !cfg.PaystackBypassIPCheck {
			if _, ok := allowedPaystackIPs[clientIP]; !ok {
				slog.Warn("Paystack webhook rejected: unauthorized sender IP", "ip", clientIP)
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
					"error":   "UNAUTHORIZED_WEBHOOK_IP",
					"message": "Origin IP address is not an authorized Paystack gateway node",
				})
				return
			}
		}

		// 2. Signature verification header
		providedSig := c.GetHeader("x-paystack-signature")
		if providedSig == "" {
			slog.Warn("Paystack webhook rejected: missing signature header")
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"error":   "MISSING_SIGNATURE",
				"message": "Header x-paystack-signature is required",
			})
			return
		}

		// 3. Read raw body bytes
		bodyBytes, err := io.ReadAll(c.Request.Body)
		if err != nil {
			c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{
				"error":   "FAILED_TO_READ_PAYLOAD",
				"message": "Unable to read request payload",
			})
			return
		}
		// Reset body for downstream binding
		c.Request.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

		// 4. Compute HMAC-SHA512(secret_key, raw_body)
		mac := hmac.New(sha512.New, []byte(cfg.PaystackSecretKey))
		mac.Write(bodyBytes)
		expectedSig := hex.EncodeToString(mac.Sum(nil))

		// 5. Constant-time comparison
		if !hmac.Equal([]byte(strings.ToLower(providedSig)), []byte(strings.ToLower(expectedSig))) {
			slog.Warn("Paystack HMAC-SHA512 signature mismatch",
				"provided", providedSig,
				"expected", expectedSig,
			)
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{
				"error":   "INVALID_PAYSTACK_SIGNATURE",
				"message": "Payload integrity verification failed",
			})
			return
		}

		// Store verified body in context for quick access
		c.Set("raw_webhook_body", bodyBytes)
		c.Next()
	}
}

