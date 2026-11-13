package services

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/boronlabs/m-tap-backend/internal/config"
)

// PaystackClient defines clearing operations for Mobile Money charge prompts.
type PaystackClient interface {
	ChargeMobileMoney(ctx context.Context, req ChargeRequest) (*ChargeResponse, error)
}

type ChargeRequest struct {
	AmountPesewas int64  `json:"amount_pesewas"`
	PhoneNumber   string `json:"phone_number"` // E.164 e.g. 233240000000
	Reference     string `json:"reference"`
	Provider      string `json:"provider"` // mtn, vod, tgo
}

type ChargeResponse struct {
	Status      bool   `json:"status"`
	Message     string `json:"message"`
	Reference   string `json:"reference"`
	DisplayText string `json:"display_text"`
}

type paystackClient struct {
	cfg        *config.Config
	httpClient *http.Client
}

func NewPaystackClient(cfg *config.Config) PaystackClient {
	return &paystackClient{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

// GhanaTelcoProvider resolves the telco provider code from Ghanaian MSISDN prefix.
func GhanaTelcoProvider(phoneNumber string) string {
	// Simple prefix mapper: 23324, 23354, 23355, 23359 -> mtn; 23320, 23350 -> vod; 23327, 23357, 23326 -> tgo
	clean := phoneNumber
	if len(clean) >= 5 {
		prefix := clean[:5]
		switch prefix {
		case "23324", "23354", "23355", "23359", "23325":
			return "mtn"
		case "23320", "23350":
			return "vod"
		case "23327", "23357", "23326":
			return "tgo"
		}
	}
	return "mtn" // Default to MTN Mobile Money
}

func (p *paystackClient) ChargeMobileMoney(ctx context.Context, req ChargeRequest) (*ChargeResponse, error) {
	if p.cfg.MockPaystackSuccess {
		slog.Info("[MOCK PAYSTACK] Mobile Money Charge prompt triggered",
			"reference", req.Reference,
			"phone_number", req.PhoneNumber,
			"amount_pesewas", req.AmountPesewas,
		)
		return &ChargeResponse{
			Status:      true,
			Message:     "Charge initiated (Mock Mode)",
			Reference:   req.Reference,
			DisplayText: "Please authorize the prompt on your phone (Mock)",
		}, nil
	}

	// Format phone to local 10-digit format for Paystack Ghana MoMo (e.g. 054..., 024..., 055...)
	momoPhone := req.PhoneNumber
	if strings.HasPrefix(momoPhone, "233") && len(momoPhone) == 12 {
		momoPhone = "0" + momoPhone[3:]
	}

	provider := req.Provider

	// In Paystack Test Mode (sk_test_...), Paystack requires specific test numbers:
	// 0551234987 (MTN), 0551234988 (Vodafone/Telecel), 0551234989 (AT).
	// If a non-test number is passed with sk_test_, Paystack rejects with HTTP 400.
	// Auto-route to 0551234987 in test mode so testing works seamlessly.
	if strings.HasPrefix(p.cfg.PaystackSecretKey, "sk_test_") {
		if momoPhone != "0551234987" && momoPhone != "0551234988" && momoPhone != "0551234989" {
			slog.Info("Paystack test mode detected: using Paystack test MoMo number",
				"original_phone", momoPhone,
				"test_phone", "0551234987",
			)
			momoPhone = "0551234987"
			provider = "mtn"
		}
	}

	payload := map[string]interface{}{
		"amount":    req.AmountPesewas, // minor units
		"email":     fmt.Sprintf("%s@m-tap.boronlabs.io", req.PhoneNumber),
		"currency":  "GHS",
		"reference": req.Reference,
		"mobile_money": map[string]string{
			"phone":    momoPhone,
			"provider": provider,
		},
	}

	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal Paystack charge request: %w", err)
	}

	url := fmt.Sprintf("%s/charge", p.cfg.PaystackBaseURL)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewBuffer(jsonBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create Paystack HTTP request: %w", err)
	}

	httpReq.Header.Set("Authorization", fmt.Sprintf("Bearer %s", p.cfg.PaystackSecretKey))
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := p.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("failed to execute Paystack charge request: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read Paystack response body: %w", err)
	}

	var rawResp struct {
		Status  bool   `json:"status"`
		Message string `json:"message"`
		Data    struct {
			Reference   string `json:"reference"`
			DisplayText string `json:"display_text"`
			Status      string `json:"status"`
		} `json:"data"`
	}

	if err := json.Unmarshal(bodyBytes, &rawResp); err != nil {
		return nil, fmt.Errorf("failed to decode Paystack response: %w (body: %s)", err, string(bodyBytes))
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 || !rawResp.Status {
		return nil, fmt.Errorf("Paystack charge rejected (HTTP %d): %s", resp.StatusCode, rawResp.Message)
	}

	displayText := rawResp.Data.DisplayText
	if displayText == "" {
		displayText = "Please authorize the Mobile Money prompt on your phone"
	}

	return &ChargeResponse{
		Status:      rawResp.Status,
		Message:     rawResp.Message,
		Reference:   rawResp.Data.Reference,
		DisplayText: displayText,
	}, nil
}

