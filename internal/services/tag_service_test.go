package services

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestTagValidationAndOTPHashing(t *testing.T) {
	t.Run("Valid Ghanaian E.164 phone numbers", func(t *testing.T) {
		validPhones := []string{"233241234567", "233549876543", "233201122334"}
		for _, p := range validPhones {
			if !phoneRegex.MatchString(p) {
				t.Errorf("Expected phone %s to match regex", p)
			}
		}
	})

	t.Run("Invalid phone numbers rejected", func(t *testing.T) {
		invalidPhones := []string{"0241234567", "+233241234567", "2348012345678", "23324123"}
		for _, p := range invalidPhones {
			if phoneRegex.MatchString(p) {
				t.Errorf("Expected invalid phone %s to be rejected", p)
			}
		}
	})

	t.Run("Valid 7-byte hexadecimal Tag UID", func(t *testing.T) {
		validTags := []string{"04A38CF2196880", "04a38cf2196880", "11223344556677"}
		for _, tag := range validTags {
			if !tagHexRegex.MatchString(tag) {
				t.Errorf("Expected tag %s to be valid", tag)
			}
		}
	})

	t.Run("Invalid Tag UIDs rejected", func(t *testing.T) {
		invalidTags := []string{"04A38CF2", "04A38CF219688000", "04A38CF219688G"}
		for _, tag := range invalidTags {
			if tagHexRegex.MatchString(tag) {
				t.Errorf("Expected invalid tag %s to be rejected", tag)
			}
		}
	})

	t.Run("SHA-256 OTP Hash Determinism", func(t *testing.T) {
		otp := "482910"
		h1 := sha256.Sum256([]byte(otp))
		hex1 := hex.EncodeToString(h1[:])

		h2 := sha256.Sum256([]byte(otp))
		hex2 := hex.EncodeToString(h2[:])

		if hex1 != hex2 {
			t.Fatalf("OTP hashes must match")
		}
	})
}

