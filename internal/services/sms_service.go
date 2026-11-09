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
	Number    string `json:"number"`
	Status    string `json:"status"`
	Cost      string `json:"cost"`
	MessageID string `json:"messageId"`
}

type atMessageResponse struct {
	SMSMessageData struct {
		Message    string                `json:"Message"`
		Recipients []atRecipientResponse `json:"Recipients"`
	} `json:"SMSMessageData"`
}

func (s *africaTalkingService) SendOTP(ctx context.Context, phoneNumber, otpCode string) error {
	msg := fmt.Sprintf("Your M-Tap activation code is: %s. Valid for 5 minutes. Boron Labs.", otpCode)

	// If mock mode is enabled, log OTP and return successfully
	if s.cfg.MockSMS {
		slog.Info("[MOCK AFRICA'S TALKING SMS] Dispatching proximity activation OTP",
			"phone_number", phoneNumber,
			"otp", otpCode,
			"msg", msg,
		)
		return nil
	}

	// Normalize E.164 phone number
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

	// Sandbox does not permit custom alphanumeric sender IDs
	if !s.cfg.AfricaTalkingSandbox && s.cfg.AfricaTalkingSenderID != "" {
		formData.Set("from", s.cfg.AfricaTalkingSenderID)
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

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("AT SMS request failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var atResp atMessageResponse
	if err := json.Unmarshal(bodyBytes, &atResp); err != nil {
		slog.Warn("Failed to parse Africa's Talking JSON response, raw body logged", "raw", string(bodyBytes))
		return nil
	}

	slog.Info("Africa's Talking SMS dispatched successfully",
		"phone", normalizedPhone,
		"status", atResp.SMSMessageData.Message,
	)

	return nil
}

