package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"

	"github.com/hibiken/asynq"
	"github.com/boronlabs/m-tap-backend/internal/services"
	"github.com/boronlabs/m-tap-backend/internal/tasks"
)

type SMSProcessor struct {
	smsService services.SMSService
}

func NewSMSProcessor(smsService services.SMSService) *SMSProcessor {
	return &SMSProcessor{
		smsService: smsService,
	}
}

func (p *SMSProcessor) ProcessTask(ctx context.Context, t *asynq.Task) error {
	var payload tasks.SendOTPPayload
	if err := json.Unmarshal(t.Payload(), &payload); err != nil {
		return fmt.Errorf("failed to unmarshal SMS task payload: %w", err)
	}

	slog.Info("Worker executing SMS OTP delivery",
		"tag_uid", payload.TagUID,
		"phone_number", payload.PhoneNumber,
	)

	if err := p.smsService.SendOTP(ctx, payload.PhoneNumber, payload.OTPCode); err != nil {
		slog.Error("Failed to deliver SMS OTP via Africa's Talking, triggering retry",
			"error", err,
			"phone_number", payload.PhoneNumber,
		)
		return err // Causes Asynq to backoff and retry up to MaxRetry
	}

	return nil
}

