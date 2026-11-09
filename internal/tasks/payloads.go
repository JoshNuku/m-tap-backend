package tasks

import (
	"encoding/json"
	"time"

	"github.com/hibiken/asynq"
)

// Queue definitions
const (
	QueuePayments      = "payments"
	QueueNotifications = "notifications"
	QueueDefault       = "default"
)

// Task Type definitions
const (
	TypePaystackWebhookProcess = "payment:paystack:webhook"
	TypeSendOTP                = "sms:otp:send"
)

// PaystackWebhookPayload represents the raw payload pushed to the payments queue.
type PaystackWebhookPayload struct {
	Event      string          `json:"event"`
	RawData    json.RawMessage `json:"data"`
	ReceivedAt time.Time       `json:"received_at"`
}

// SendOTPPayload represents the async SMS notification payload.
type SendOTPPayload struct {
	TagUID      string `json:"tag_uid"`
	PhoneNumber string `json:"phone_number"`
	OTPCode     string `json:"otp_code"`
}

// NewPaystackWebhookTask creates an Asynq task for Paystack webhook processing.
func NewPaystackWebhookTask(event string, rawData json.RawMessage) (*asynq.Task, error) {
	payload, err := json.Marshal(PaystackWebhookPayload{
		Event:      event,
		RawData:    rawData,
		ReceivedAt: time.Now(),
	})
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(TypePaystackWebhookProcess, payload, asynq.Queue(QueuePayments), asynq.MaxRetry(5)), nil
}

// NewSendOTPTask creates an Asynq task for asynchronous SMS OTP delivery.
func NewSendOTPTask(tagUID, phoneNumber, otpCode string) (*asynq.Task, error) {
	payload, err := json.Marshal(SendOTPPayload{
		TagUID:      tagUID,
		PhoneNumber: phoneNumber,
		OTPCode:     otpCode,
	})
	if err != nil {
		return nil, err
	}
	return asynq.NewTask(TypeSendOTP, payload, asynq.Queue(QueueNotifications), asynq.MaxRetry(3)), nil
}

