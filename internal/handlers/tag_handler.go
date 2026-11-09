package handlers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/boronlabs/m-tap-backend/internal/models"
	"github.com/boronlabs/m-tap-backend/internal/services"
)

type TagHandler struct {
	tagService services.TagService
}

func NewTagHandler(tagService services.TagService) *TagHandler {
	return &TagHandler{
		tagService: tagService,
	}
}

type InitiatePairingRequest struct {
	TagUID      string `json:"tag_uid" binding:"required"`
	PhoneNumber string `json:"phone_number" binding:"required"`
}

type VerifyOTPRequest struct {
	TagUID string `json:"tag_uid" binding:"required"`
	OTP    string `json:"otp" binding:"required,len=6"`
}

type RevokeTagRequest struct {
	TagUID string `json:"tag_uid" binding:"required"`
	Reason string `json:"reason"`
}

// InitiatePairing triggers customer tag onboarding by sending a cryptographically secure OTP via Africa's Talking.
func (h *TagHandler) InitiatePairing(c *gin.Context) {
	var req InitiatePairingRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "INVALID_REQUEST_PAYLOAD",
			"message": err.Error(),
		})
		return
	}

	err := h.tagService.InitiatePairing(c.Request.Context(), req.TagUID, req.PhoneNumber, c.ClientIP())
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "PAIRING_INITIATION_FAILED",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "OTP activation code dispatched via SMS. Valid for 5 minutes.",
		"tag_uid": req.TagUID,
		"status":  models.StatusPendingActivation,
	})
}

// VerifyOTP validates the 6-digit OTP code against Redis, activating the tag.
func (h *TagHandler) VerifyOTP(c *gin.Context) {
	var req VerifyOTPRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "INVALID_REQUEST_PAYLOAD",
			"message": err.Error(),
		})
		return
	}

	err := h.tagService.VerifyOTP(c.Request.Context(), req.TagUID, req.OTP)
	if err != nil {
		c.JSON(http.StatusUnauthorized, gin.H{
			"error":   "OTP_VERIFICATION_FAILED",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "NFC tag successfully paired and activated for proximity payments",
		"tag_uid": req.TagUID,
		"status":  models.StatusActive,
	})
}

// Revoke handles emergency kill-switch revocation for lost, stolen, or compromised tags.
func (h *TagHandler) Revoke(c *gin.Context) {
	var req RevokeTagRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "INVALID_REQUEST_PAYLOAD",
			"message": err.Error(),
		})
		return
	}

	reason := req.Reason
	if reason == "" {
		reason = "Emergency self-service customer revocation"
	}

	err := h.tagService.RevokeTag(c.Request.Context(), req.TagUID, reason, models.ActorCustomer, c.ClientIP())
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{
			"error":   "REVOCATION_FAILED",
			"message": err.Error(),
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"message": "NFC tag has been permanently revoked and evicted from switching cache",
		"tag_uid": req.TagUID,
		"status":  models.StatusRevoked,
	})
}

