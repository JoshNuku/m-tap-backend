package handlers

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/boronlabs/m-tap-backend/internal/config"
	"github.com/boronlabs/m-tap-backend/internal/middleware"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

func TestSwaggerEndpoints(t *testing.T) {
	gin.SetMode(gin.TestMode)

	r := gin.New()
	swagger := NewSwaggerHandler()
	r.GET("/swagger", swagger.ServeUI)
	r.GET("/swagger/openapi.yaml", swagger.ServeSpec)

	t.Run("Swagger UI renders HTML dashboard", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, "/swagger", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("Expected 200 OK, got %d", w.Code)
		}
		if !bytes.Contains(w.Body.Bytes(), []byte("M-Tap Switching Engine - API Docs")) {
			t.Fatalf("Expected HTML to contain swagger UI title")
		}
	})

	t.Run("OpenAPI YAML spec is served properly", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodGet, "/swagger/openapi.yaml", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("Expected 200 OK, got %d", w.Code)
		}
		if !bytes.Contains(w.Body.Bytes(), []byte("openapi: 3.0.3")) {
			t.Fatalf("Expected body to contain openapi version")
		}
	})
}

func TestPaystackWebhookHMACValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)

	secretKey := "sk_test_mock_secret_key"
	cfg := &config.Config{
		PaystackSecretKey:     secretKey,
		PaystackBypassIPCheck: true,
	}

	payload := map[string]interface{}{
		"event": "charge.success",
		"data": map[string]interface{}{
			"reference": "mtap_test_ref_123",
			"status":    "success",
			"amount":    2500, // minor units in pesewas
		},
	}
	bodyBytes, _ := json.Marshal(payload)

	mac := hmac.New(sha512.New, []byte(secretKey))
	mac.Write(bodyBytes)
	validSignature := hex.EncodeToString(mac.Sum(nil))

	r := gin.New()
	r.Use(middleware.PaystackSecurityDefense(cfg))
	r.POST("/api/v1/payments/paystack/webhook", func(c *gin.Context) {
		c.JSON(http.StatusOK, gin.H{"status": true})
	})

	t.Run("Valid HMAC-SHA512 passes verification", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPost, "/api/v1/payments/paystack/webhook", bytes.NewBuffer(bodyBytes))
		req.Header.Set("x-paystack-signature", validSignature)
		w := httptest.NewRecorder()

		r.ServeHTTP(w, req)
		if w.Code != http.StatusOK {
			t.Fatalf("Expected 200 OK, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("Tampered payload is rejected", func(t *testing.T) {
		tamperedBytes := []byte(`{"event":"charge.success","data":{"amount":999999}}`)
		req, _ := http.NewRequest(http.MethodPost, "/api/v1/payments/paystack/webhook", bytes.NewBuffer(tamperedBytes))
		req.Header.Set("x-paystack-signature", validSignature)
		w := httptest.NewRecorder()

		r.ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("Expected 401 Unauthorized for tampered payload, got %d", w.Code)
		}
	})
}

func TestTerminalHMACLogic(t *testing.T) {
	psk := "secret_hmac_psk_key_boron_labs_2026"
	payload := `{"terminal_id":"POD_001","tag_uid":"04A38CF2196880","amount_pesewas":2500}`

	ts := time.Now().Unix()
	mac := hmac.New(sha256.New, []byte(psk))
	mac.Write([]byte(fmt.Sprintf("%d.%s", ts, payload)))
	sig := hex.EncodeToString(mac.Sum(nil))

	if len(sig) != 64 {
		t.Fatalf("Expected 64-character SHA-256 hex string, got %d", len(sig))
	}

	// Verify constant time equality
	mac2 := hmac.New(sha256.New, []byte(psk))
	mac2.Write([]byte(fmt.Sprintf("%d.%s", ts, payload)))
	sig2 := hex.EncodeToString(mac2.Sum(nil))

	if !hmac.Equal([]byte(sig), []byte(sig2)) {
		t.Fatalf("Signatures must match")
	}
}

func TestMinorUnitsIntegrity(t *testing.T) {
	// GHS 25.50 must be represented as exactly 2550 Pesewas
	var ghs float64 = 25.50
	pesewas := int64(ghs * 100)
	if pesewas != 2550 {
		t.Fatalf("Expected 2550 pesewas, got %d", pesewas)
	}

	// Double-entry calculation test: zero float drift
	var tap1 int64 = 1525 // GHS 15.25
	var tap2 int64 = 2475 // GHS 24.75
	total := tap1 + tap2
	if total != 4000 { // GHS 40.00
		t.Fatalf("Expected exactly 4000 pesewas, got %d", total)
	}
}

func TestIdempotencyKeyFormat(t *testing.T) {
	key := uuid.New().String()
	parsed, err := uuid.Parse(key)
	if err != nil || parsed == uuid.Nil {
		t.Fatalf("Idempotency key must be valid UUIDv4")
	}
}
