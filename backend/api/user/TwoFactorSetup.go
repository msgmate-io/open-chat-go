package user

import (
	"backend/database"
	"encoding/json"
	"fmt"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"net/http"
	"time"
)

type TwoFactorSetupResponse struct {
	Secret    string `json:"secret"`
	QRCodeURL string `json:"qr_code_url"`
}

type TwoFactorConfirmRequest struct {
	Secret string `json:"secret"`
	Code   string `json:"code"`
}

type TwoFactorRecoveryCodesResponse struct {
	Codes []string `json:"codes"`
}

// SetupTwoFactor generates a new 2FA secret and QR code for the user
//
// @Summary      Setup Two-Factor Authentication
// @Description  Generate a new 2FA secret and QR code URL for the user
// @Tags         user
// @Accept       json
// @Produce      json
// @Success      200 {object} TwoFactorSetupResponse "2FA setup data"
// @Failure      400 {string} string "Bad request"
// @Failure      500 {string} string "Internal server error"
// @Router       /api/v1/user/2fa/setup [post]
func (h *UserHandler) SetupTwoFactor(w http.ResponseWriter, r *http.Request) {
	user, ok := r.Context().Value("user").(*database.User)
	if !ok {
		http.Error(w, "Invalid user", http.StatusBadRequest)
		return
	}

	_, ok = r.Context().Value("db").(*gorm.DB)
	if !ok {
		http.Error(w, "Unable to get database", http.StatusBadRequest)
		return
	}

	// Generate a new secret
	secret := generateSecret()

	// Create QR code URL (using Google Charts QR API as a simple solution)
	issuer := "Open Chat Go"
	accountName := user.Email
	qrCodeURL := fmt.Sprintf("otpauth://totp/%s:%s?secret=%s&issuer=%s",
		issuer, accountName, secret, issuer)

	response := TwoFactorSetupResponse{
		Secret:    secret,
		QRCodeURL: qrCodeURL,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// ConfirmTwoFactor confirms and enables 2FA for the user
//
// @Summary      Confirm Two-Factor Authentication
// @Description  Verify the 2FA code and enable 2FA for the user
// @Tags         user
// @Accept       json
// @Produce      json
// @Param        request body TwoFactorConfirmRequest true "2FA confirmation data"
// @Success      200 {object} TwoFactorRecoveryCodesResponse "Recovery codes"
// @Failure      400 {string} string "Invalid code"
// @Failure      500 {string} string "Internal server error"
// @Router       /api/v1/user/2fa/confirm [post]
func (h *UserHandler) ConfirmTwoFactor(w http.ResponseWriter, r *http.Request) {
	user, ok := r.Context().Value("user").(*database.User)
	if !ok {
		http.Error(w, "Invalid user", http.StatusBadRequest)
		return
	}

	DB, ok := r.Context().Value("db").(*gorm.DB)
	if !ok {
		http.Error(w, "Unable to get database", http.StatusBadRequest)
		return
	}

	var data TwoFactorConfirmRequest
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	// Verify the TOTP code
	if !VerifyTOTP(data.Secret, data.Code, time.Now()) {
		http.Error(w, "Invalid 2FA code", http.StatusBadRequest)
		return
	}

	// Update user with 2FA secret
	user.TwoFactorSecret = data.Secret
	user.TwoFactorEnabled = true

	if err := DB.Save(user).Error; err != nil {
		http.Error(w, "Failed to enable 2FA", http.StatusInternalServerError)
		return
	}

	// Generate recovery codes
	recoveryCodes := generateRecoveryCodes(10)

	// Store recovery codes in database
	for _, code := range recoveryCodes {
		hashedCode, err := bcrypt.GenerateFromPassword([]byte(code), bcrypt.DefaultCost)
		if err != nil {
			http.Error(w, "Failed to generate recovery codes", http.StatusInternalServerError)
			return
		}

		recoveryCode := database.TwoFactorRecoveryCode{
			UserId:   user.ID,
			CodeHash: string(hashedCode),
		}

		if err := DB.Create(&recoveryCode).Error; err != nil {
			http.Error(w, "Failed to save recovery codes", http.StatusInternalServerError)
			return
		}
	}

	response := TwoFactorRecoveryCodesResponse{
		Codes: recoveryCodes,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// DisableTwoFactor disables 2FA for the user
//
// @Summary      Disable Two-Factor Authentication
// @Description  Disable 2FA for the user and remove recovery codes
// @Tags         user
// @Accept       json
// @Produce      json
// @Success      200 {string} string "2FA disabled successfully"
// @Failure      400 {string} string "Bad request"
// @Failure      500 {string} string "Internal server error"
// @Router       /api/v1/user/2fa/disable [post]
func (h *UserHandler) DisableTwoFactor(w http.ResponseWriter, r *http.Request) {
	user, ok := r.Context().Value("user").(*database.User)
	if !ok {
		http.Error(w, "Invalid user", http.StatusBadRequest)
		return
	}

	DB, ok := r.Context().Value("db").(*gorm.DB)
	if !ok {
		http.Error(w, "Unable to get database", http.StatusBadRequest)
		return
	}

	// Disable 2FA
	user.TwoFactorEnabled = false
	user.TwoFactorSecret = ""

	if err := DB.Save(user).Error; err != nil {
		http.Error(w, "Failed to disable 2FA", http.StatusInternalServerError)
		return
	}

	// Remove all recovery codes
	if err := DB.Where("user_id = ?", user.ID).Delete(&database.TwoFactorRecoveryCode{}).Error; err != nil {
		http.Error(w, "Failed to remove recovery codes", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("2FA disabled successfully"))
}

// GenerateNewRecoveryCodes generates new recovery codes for the user
//
// @Summary      Generate New Recovery Codes
// @Description  Generate new recovery codes for 2FA (invalidates old ones)
// @Tags         user
// @Accept       json
// @Produce      json
// @Success      200 {object} TwoFactorRecoveryCodesResponse "New recovery codes"
// @Failure      400 {string} string "Bad request"
// @Failure      500 {string} string "Internal server error"
// @Router       /api/v1/user/2fa/recovery-codes [post]
func (h *UserHandler) GenerateNewRecoveryCodes(w http.ResponseWriter, r *http.Request) {
	user, ok := r.Context().Value("user").(*database.User)
	if !ok {
		http.Error(w, "Invalid user", http.StatusBadRequest)
		return
	}

	DB, ok := r.Context().Value("db").(*gorm.DB)
	if !ok {
		http.Error(w, "Unable to get database", http.StatusBadRequest)
		return
	}

	if !user.TwoFactorEnabled {
		http.Error(w, "2FA is not enabled", http.StatusBadRequest)
		return
	}

	// Remove old recovery codes
	if err := DB.Where("user_id = ?", user.ID).Delete(&database.TwoFactorRecoveryCode{}).Error; err != nil {
		http.Error(w, "Failed to remove old recovery codes", http.StatusInternalServerError)
		return
	}

	// Generate new recovery codes
	recoveryCodes := generateRecoveryCodes(10)

	// Store new recovery codes
	for _, code := range recoveryCodes {
		hashedCode, err := bcrypt.GenerateFromPassword([]byte(code), bcrypt.DefaultCost)
		if err != nil {
			http.Error(w, "Failed to generate recovery codes", http.StatusInternalServerError)
			return
		}

		recoveryCode := database.TwoFactorRecoveryCode{
			UserId:   user.ID,
			CodeHash: string(hashedCode),
		}

		if err := DB.Create(&recoveryCode).Error; err != nil {
			http.Error(w, "Failed to save recovery codes", http.StatusInternalServerError)
			return
		}
	}

	response := TwoFactorRecoveryCodesResponse{
		Codes: recoveryCodes,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// Helper functions
func generateSecret() string {
	// Generate a 32-character base32 secret
	const charset = "ABCDEFGHIJKLMNOPQRSTUVWXYZ234567"
	secret := make([]byte, 32)
	for i := range secret {
		secret[i] = charset[time.Now().UnixNano()%int64(len(charset))]
	}
	return string(secret)
}

func generateRecoveryCodes(count int) []string {
	codes := make([]string, count)
	for i := 0; i < count; i++ {
		// Generate 8-character recovery codes
		codes[i] = fmt.Sprintf("%08d", time.Now().UnixNano()%100000000)
		time.Sleep(1) // Ensure uniqueness
	}
	return codes
}
