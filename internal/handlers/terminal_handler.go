package handlers

import (
	"encoding/json"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	"github.com/boronlabs/m-tap-backend/internal/middleware"
	"github.com/boronlabs/m-tap-backend/internal/models"
	"github.com/boronlabs/m-tap-backend/internal/services"
)

type TerminalHandler struct {
	paymentService services.PaymentService
	rdb            *redis.Client
}

func NewTerminalHandler(paymentService services.PaymentService, rdb *redis.Client) *TerminalHandler {
	return &TerminalHandler{
		paymentService: paymentService,
		rdb:            rdb,
	}
}

type TapRequest struct {
	TerminalID    string `json:"terminal_id" binding:"required"`
	TagUID        string `json:"tag_uid" binding:"required"`
	AmountPesewas int64  `json:"amount_pesewas" binding:"required,gt=0"`
}

// HandleTap processes an NFC tap transaction from an authenticated POS terminal.
func (h *TerminalHandler) HandleTap(c *gin.Context) {
	// Retrieve authenticated terminal from HMAC middleware
	terminalVal, exists := c.Get(middleware.ContextTerminalKey)
	if !exists {
		c.JSON(http.StatusUnauthorized, gin.H{"error": "UNAUTHENTICATED_TERMINAL"})
		return
	}
	terminal := terminalVal.(models.Terminal)

	var req TapRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "INVALID_REQUEST_BODY",
			"message": err.Error(),
		})
		return
	}

	// Verify terminal ID in payload matches authenticated terminal
	if req.TerminalID != terminal.TerminalIdentifier {
		c.JSON(http.StatusForbidden, gin.H{
			"error":   "TERMINAL_IDENTITY_MISMATCH",
			"message": "Authenticated terminal header does not match payload terminal_id",
		})
		return
	}

	result, err := h.paymentService.ProcessTap(
		c.Request.Context(),
		terminal.ID,
		terminal.TerminalIdentifier,
		req.TagUID,
		req.AmountPesewas,
	)
	if err != nil {
		// Release idempotency lock so subsequent retries are permitted
		if redisKey, ok := c.Get("idempotency_redis_key"); ok {
			middleware.ReleaseIdempotencyLock(c.Request.Context(), h.rdb, redisKey.(string))
		}

		c.JSON(http.StatusUnprocessableEntity, gin.H{
			"error":   "TAP_PROCESSING_REJECTED",
			"message": err.Error(),
		})
		return
	}

	// Cache successful tap state in idempotency key
	if redisKey, ok := c.Get("idempotency_redis_key"); ok {
		resBytes, _ := json.Marshal(result)
		middleware.CacheIdempotencyResult(c.Request.Context(), h.rdb, redisKey.(string), string(resBytes))
	}

	// Return 202 Accepted with internal transaction ID and status
	c.JSON(http.StatusAccepted, gin.H{
		"transaction_id":     result.TransactionID,
		"external_reference": result.ExternalReference,
		"status":            result.Status,
		"amount_pesewas":     result.AmountPesewas,
		"message":           result.Message,
	})
}

