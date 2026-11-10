package services

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/boronlabs/m-tap-backend/internal/config"
)

// SMSService defines the interface for delivering proximity activation OTPs.
type SMSService interface {
	SendOTP(ctx context.Context, phoneNumber, otpCode string) error
}

type africaTalkingService struct {
	cfg        *config.Config
	httpClient *http.Client
}

// NewAfricaTalkingService initializes the Africa's Talking SMS provider.
func NewAfricaTalkingService(cfg *config.Config) SMSService {
	return &africaTalkingService{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
	}
}

type atRecipientResponse struct {
	Number     string `json:"number"`
	Status     string `json:"status"`
	Cost       string `json:"cost"`
	MessageID  string `json:"messageId"`
	StatusCode int    `json:"statusCode"`
}

type atMessageResponse struct {
	SMSMessageData struct {
		Message    string                `json:"Message"`
		Recipients []atRecipientResponse `json:"Recipients"`
	} `json:"SMSMessageData"`
}

func (s *africaTalkingService) SendOTP(ctx context.Context, phoneNumber, otpCode string) error {
	msg := fmt.Sprintf("Your M-Tap proximity activation code is: %s. Valid for 5 minutes. Boron Labs.", otpCode)

	// Always emit structured log so developer/tester has immediate visibility in terminal
	slog.Info("Proximity activation OTP generated",
		"phone_number", phoneNumber,
		"otp", otpCode,
		"mock_sms", s.cfg.MockSMS,
	)

	// If mock mode is enabled, skip external HTTP call
	if s.cfg.MockSMS {
		slog.Info("[MOCK SMS ACTIVATED] Skipping external Africa's Talking dispatch",
			"recipient", phoneNumber,
			"otp", otpCode,
		)
		return nil
	}

	// Format destination with international '+' prefix required by Africa's Talking
	normalizedPhone := phoneNumber
	if !strings.HasPrefix(normalizedPhone, "+") {
		normalizedPhone = "+" + normalizedPhone
	}

	endpoint := "https://api.africastalking.com/version1/messaging"
	if s.cfg.AfricaTalkingSandbox {
		endpoint = "https://api.sandbox.africastalking.com/version1/messaging"
	}

	formData := url.Values{}
	formData.Set("username", s.cfg.AfricaTalkingUsername)
	formData.Set("to", normalizedPhone)
	formData.Set("message", msg)

	// Only pass 'from' if an alphanumeric sender ID is registered and approved
	if !s.cfg.AfricaTalkingSandbox && strings.TrimSpace(s.cfg.AfricaTalkingSenderID) != "" {
		formData.Set("from", strings.TrimSpace(s.cfg.AfricaTalkingSenderID))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, strings.NewReader(formData.Encode()))
	if err != nil {
		return fmt.Errorf("failed to create AT SMS request: %w", err)
	}

	req.Header.Set("apiKey", s.cfg.AfricaTalkingAPIKey)
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Accept", "application/json")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to execute AT SMS request: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read AT SMS response: %w", err)
	}

	slog.Info("Africa's Talking API Raw Response",
		"http_status", resp.StatusCode,
		"raw_body", string(bodyBytes),
	)

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("AT SMS request failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var atResp atMessageResponse
	if err := json.Unmarshal(bodyBytes, &atResp); err != nil {
		slog.Warn("Failed to parse Africa's Talking JSON response", "raw", string(bodyBytes))
		return nil
	}

	// Verify recipient delivery status
	if len(atResp.SMSMessageData.Recipients) == 0 {
		slog.Warn("Africa's Talking returned zero recipients (check account balance or sender ID)",
			"message", atResp.SMSMessageData.Message,
			"raw", string(bodyBytes),
		)
		return fmt.Errorf("Africa's Talking SMS not accepted: %s", atResp.SMSMessageData.Message)
	}

	recipient := atResp.SMSMessageData.Recipients[0]
	if recipient.Status != "Success" {
		slog.Warn("Africa's Talking SMS delivery non-success status",
			"status", recipient.Status,
			"cost", recipient.Cost,
			"phone", recipient.Number,
		)
	} else {
		slog.Info("Africa's Talking SMS delivered to telco gateway",
			"recipient", recipient.Number,
			"message_id", recipient.MessageID,
			"cost", recipient.Cost,
		)
	}

	return nil
}
