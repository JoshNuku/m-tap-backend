package services

import (
	"crypto/sha256"
	"encoding/hex"
	"testing"
)

func TestTagValidationAndOTPHashing(t *testing.T) {
	t.Run("Valid Ghanaian phone formats normalized", func(t *testing.T) {
		testCases := []struct {
			input    string
			expected string
		}{
			{input: "+233241234567", expected: "233241234567"},
			{input: "0241234567", expected: "233241234567"},
			{input: "233241234567", expected: "233241234567"},
			{input: "0549876543", expected: "233549876543"},
			{input: "0201122334", expected: "233201122334"},
		}

		for _, tc := range testCases {
			normalized, err := NormalizeAndValidateGhanaPhone(tc.input)
			if err != nil {
				t.Fatalf("Unexpected error for %s: %v", tc.input, err)
			}
			if normalized != tc.expected {
				t.Fatalf("Expected %s, got %s", tc.expected, normalized)
			}
		}
	})

	t.Run("Invalid phone numbers rejected", func(t *testing.T) {
		invalidPhones := []string{"2348012345678", "0123456789", "23324123", "024123456789", "phone_number"}
		for _, p := range invalidPhones {
			_, err := NormalizeAndValidateGhanaPhone(p)
			if err == nil {
				t.Errorf("Expected invalid phone %s to be rejected", p)
			}
		}
	})

	t.Run("Valid 7-byte hexadecimal Tag UID", func(t *testing.T) {
		validTags := []string{"04A38CF2196880", "04a38cf2196880", "11223344556677"}
		for _, tag := range validTags {
			cleaned, err := NormalizeAndValidateTagUID(tag)
			if err != nil {
				t.Errorf("Expected tag %s to be valid, got err: %v", tag, err)
			}
			if len(cleaned) != 14 {
				t.Errorf("Expected 14 chars, got %d", len(cleaned))
			}
		}
	})

	t.Run("Invalid Tag UIDs rejected", func(t *testing.T) {
		invalidTags := []string{"04A38CF2", "04A38CF219688000", "04A38CF219688G", "invalid_tag"}
		for _, tag := range invalidTags {
			_, err := NormalizeAndValidateTagUID(tag)
			if err == nil {
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
