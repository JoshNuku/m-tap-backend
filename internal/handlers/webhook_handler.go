package handlers

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"
	"github.com/boronlabs/m-tap-backend/internal/tasks"
)

type WebhookHandler struct {
	asynqClient *asynq.Client
}

func NewWebhookHandler(asynqClient *asynq.Client) *WebhookHandler {
	return &WebhookHandler{asynqClient: asynqClient}
}

type incomingWebhook struct {
	Event string          `json:"event"`
	Data  json.RawMessage `json:"data"`
}

// HandlePaystackWebhook receives, acknowledges, and enqueues Paystack asynchronous events.
func (h *WebhookHandler) HandlePaystackWebhook(c *gin.Context) {
	// Raw body is pre-verified by PaystackSecurityDefense middleware
	rawBodyVal, exists := c.Get("raw_webhook_body")
	var bodyBytes []byte
	if exists {
		bodyBytes = rawBodyVal.([]byte)
	} else {
		c.JSON(http.StatusBadRequest, gin.H{"error": "MISSING_VERIFIED_PAYLOAD"})
		return
	}

	var payload incomingWebhook
	if err := json.Unmarshal(bodyBytes, &payload); err != nil {
		slog.Error("Failed to parse verified webhook json", "error", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "MALFORMED_WEBHOOK_JSON"})
		return
	}

	// Build asynchronous task for payments worker
	task, err := tasks.NewPaystackWebhookTask(payload.Event, payload.Data)
	if err != nil {
		slog.Error("Failed to build webhook task", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "TASK_CREATION_FAILED"})
		return
	}

	// Enqueue task into payments queue
	info, err := h.asynqClient.Enqueue(task)
	if err != nil {
		slog.Error("CRITICAL: Failed to enqueue payment settlement task", "error", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "QUEUE_ENQUEUE_FAILED"})
		return
	}

	slog.Info("Paystack webhook enqueued onto payments queue",
		"task_id", info.ID,
		"event", payload.Event,
	)

	// Return fast 200 OK to satisfy Paystack SLA (<50ms)
	c.JSON(http.StatusOK, gin.H{
		"status":  true,
		"message": "Webhook acknowledged and dispatched to settlement engine",
	})
}

