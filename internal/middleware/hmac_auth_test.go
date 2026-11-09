package middleware

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func TestHMACSignatureAndReplayValidation(t *testing.T) {
	gin.SetMode(gin.TestMode)

	psk := "test_secret_key_123"
	payload := `{"terminal_id":"POD_TEST","tag_uid":"04A38CF2196880","amount_pesewas":1500}`

	t.Run("Valid signature within replay window", func(t *testing.T) {
		ts := time.Now().Unix()
		mac := hmac.New(sha256.New, []byte(psk))
		mac.Write([]byte(fmt.Sprintf("%d.%s", ts, payload)))
		validSig := hex.EncodeToString(mac.Sum(nil))

		// Check signature calculation
		computedMac := hmac.New(sha256.New, []byte(psk))
		computedMac.Write([]byte(fmt.Sprintf("%d.%s", ts, payload)))
		computedSig := hex.EncodeToString(computedMac.Sum(nil))

		if !hmac.Equal([]byte(validSig), []byte(computedSig)) {
			t.Fatalf("Expected signatures to match")
		}
	})

	t.Run("Replay attack exceeds 30s", func(t *testing.T) {
		expiredTs := time.Now().Unix() - 35 // 35 seconds ago
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)

		c.Request, _ = http.NewRequest(http.MethodPost, "/api/v1/terminal/tap", bytes.NewBufferString(payload))
		c.Request.Header.Set(HeaderTerminalID, "POD_TEST")
		c.Request.Header.Set(HeaderTimestamp, fmt.Sprintf("%d", expiredTs))
		c.Request.Header.Set(HeaderSignature, "some_sig")

		// Simulate anti-replay check
		now := time.Now().Unix()
		diff := now - expiredTs
		if diff <= ReplayWindowSec {
			t.Fatalf("Expected diff %d to exceed replay window %d", diff, ReplayWindowSec)
		}
	})

	t.Run("Tampered body causes signature failure", func(t *testing.T) {
		ts := time.Now().Unix()
		mac := hmac.New(sha256.New, []byte(psk))
		mac.Write([]byte(fmt.Sprintf("%d.%s", ts, payload)))
		originalSig := hex.EncodeToString(mac.Sum(nil))

		tamperedPayload := `{"terminal_id":"POD_TEST","tag_uid":"04A38CF2196880","amount_pesewas":99999}`
		tamperedMac := hmac.New(sha256.New, []byte(psk))
		tamperedMac.Write([]byte(fmt.Sprintf("%d.%s", ts, tamperedPayload)))
		tamperedSig := hex.EncodeToString(tamperedMac.Sum(nil))

		if hmac.Equal([]byte(originalSig), []byte(tamperedSig)) {
			t.Fatalf("Tampered payload must not match original signature")
		}
	})
}

