package middleware

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha512"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/boronlabs/m-tap-backend/internal/config"
)

func TestPaystackSecurityDefense(t *testing.T) {
	gin.SetMode(gin.TestMode)

	secretKey := "sk_test_paystack_secret_123"
	payload := `{"event":"charge.success","data":{"reference":"ref_123","status":"success"}}`

	cfg := &config.Config{
		PaystackSecretKey:     secretKey,
		PaystackBypassIPCheck: true, // test signature logic
	}

	mac := hmac.New(sha512.New, []byte(secretKey))
	mac.Write([]byte(payload))
	validSig := hex.EncodeToString(mac.Sum(nil))

	t.Run("Valid HMAC-SHA512 signature is accepted", func(t *testing.T) {
		r := gin.New()
		r.Use(PaystackSecurityDefense(cfg))
		r.POST("/webhook", func(c *gin.Context) {
			c.Status(http.StatusOK)
		})

		req, _ := http.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
		req.Header.Set("x-paystack-signature", validSig)
		w := httptest.NewRecorder()

		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("Expected 200 OK, got %d: %s", w.Code, w.Body.String())
		}
	})

	t.Run("Invalid signature is rejected with 401", func(t *testing.T) {
		r := gin.New()
		r.Use(PaystackSecurityDefense(cfg))
		r.POST("/webhook", func(c *gin.Context) {
			c.Status(http.StatusOK)
		})

		req, _ := http.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
		req.Header.Set("x-paystack-signature", "invalid_signature_hex")
		w := httptest.NewRecorder()

		r.ServeHTTP(w, req)

		if w.Code != http.StatusUnauthorized {
			t.Fatalf("Expected 401 Unauthorized, got %d", w.Code)
		}
	})

	t.Run("Missing signature header is rejected with 400", func(t *testing.T) {
		r := gin.New()
		r.Use(PaystackSecurityDefense(cfg))
		r.POST("/webhook", func(c *gin.Context) {
			c.Status(http.StatusOK)
		})

		req, _ := http.NewRequest(http.MethodPost, "/webhook", bytes.NewBufferString(payload))
		w := httptest.NewRecorder()

		r.ServeHTTP(w, req)

		if w.Code != http.StatusBadRequest {
			t.Fatalf("Expected 400 Bad Request, got %d", w.Code)
		}
	})
}

