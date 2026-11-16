package user

import (
	"backend/database"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"gorm.io/gorm"
)

// This file implements the account-side of ci#1111: one account may carry a
// password *and* N linked external identities, and both login paths land on the
// same user id.

// IdentityLinkResponse is one linked external identity, without the raw
// `subject` (an opaque provider id that the UI never needs).
type IdentityLinkResponse struct {
	UUID      string `json:"uuid"`
	Issuer    string `json:"issuer"`
	Email     string `json:"email"`
	Name      string `json:"name"`
	Source    string `json:"source"`
	LastLogin string `json:"last_login_at,omitempty"`
}

// SetPasswordRequest is the body of the authenticated set/change-password call.
type SetPasswordRequest struct {
	Password        string `json:"password"`
	CurrentPassword string `json:"current_password"`
}

// SetPassword attaches or replaces the password of the authenticated account.
//
// This is what makes an OIDC-provisioned account usable with a password: such
// accounts are created *without* a hash, and nothing else in the API could ever
// set one. When the account already has a password the current one must be
// supplied, so a hijacked session cannot silently lock the owner out.
//
//	@Summary      Set or change the account password
//	@Tags         users
//	@Accept       json
//	@Produce      json
//	@Security     SessionAuth
//	@Success      200 {string} string "Password updated"
//	@Router       /api/v1/user/password [post]
func (h *UserHandler) SetPassword(w http.ResponseWriter, r *http.Request) {
	user, ok := r.Context().Value("user").(*database.User)
	if !ok || user == nil {
		http.Error(w, "Invalid user", http.StatusBadRequest)
		return
	}
	if h.DB == nil {
		http.Error(w, "Database unavailable", http.StatusInternalServerError)
		return
	}

	var req SetPasswordRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request body", http.StatusBadRequest)
		return
	}
	if len(req.Password) < database.MinAccountPasswordLength {
		http.Error(w, "password must be at least 8 characters", http.StatusBadRequest)
		return
	}

	var stored database.User
	if err := h.DB.First(&stored, "id = ?", user.ID).Error; err != nil {
		http.Error(w, "Unable to load account", http.StatusInternalServerError)
		return
	}
	if database.AccountHasPassword(&stored) {
		if strings.TrimSpace(req.CurrentPassword) == "" {
			http.Error(w, "current_password is required to change an existing password", http.StatusBadRequest)
			return
		}
		if err := database.VerifyUserPassword(&stored, req.CurrentPassword); err != nil {
			http.Error(w, "Current password is incorrect", http.StatusForbidden)
			return
		}
	}
	if err := database.SetUserPassword(h.DB, stored.ID, req.Password); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusOK)
}

// ListIdentities returns the external identities linked to the current account.
//
//	@Summary      List linked identities
//	@Tags         users
//	@Produce      json
//	@Security     SessionAuth
//	@Success      200 {array} IdentityLinkResponse
//	@Router       /api/v1/user/identities [get]
func (h *UserHandler) ListIdentities(w http.ResponseWriter, r *http.Request) {
	user, ok := r.Context().Value("user").(*database.User)
	if !ok || user == nil {
		http.Error(w, "Invalid user", http.StatusBadRequest)
		return
	}
	links, err := database.ListUserIdentities(h.DB, user.ID)
	if err != nil {
		http.Error(w, "Unable to load linked identities", http.StatusInternalServerError)
		return
	}
	response := make([]IdentityLinkResponse, 0, len(links))
	for _, link := range links {
		item := IdentityLinkResponse{
			UUID:   link.UUID,
			Issuer: link.Issuer,
			Email:  link.Email,
			Name:   link.Name,
			Source: link.Source,
		}
		if link.LastLoginAt != nil {
			item.LastLogin = link.LastLoginAt.UTC().Format("2006-01-02T15:04:05Z")
		}
		response = append(response, item)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(response)
}

// UnlinkIdentity removes a linked identity from the current account so the
// owner can re-link it deliberately (for example after an account repair).
// Unlinking the last identity of a password-less account is refused: that would
// lock the owner out of their own account.
//
//	@Summary      Unlink an external identity
//	@Tags         users
//	@Security     SessionAuth
//	@Success      200 {string} string "Identity unlinked"
//	@Router       /api/v1/user/identities/{identity_uuid} [delete]
func (h *UserHandler) UnlinkIdentity(w http.ResponseWriter, r *http.Request) {
	user, ok := r.Context().Value("user").(*database.User)
	if !ok || user == nil {
		http.Error(w, "Invalid user", http.StatusBadRequest)
		return
	}
	identityUUID := strings.TrimSpace(r.PathValue("identity_uuid"))
	if identityUUID == "" {
		http.Error(w, "identity_uuid is required", http.StatusBadRequest)
		return
	}
	links, err := database.ListUserIdentities(h.DB, user.ID)
	if err != nil {
		http.Error(w, "Unable to load linked identities", http.StatusInternalServerError)
		return
	}
	if !database.AccountHasPassword(user) && len(links) <= 1 {
		http.Error(w, "Cannot unlink the last identity of an account without a password", http.StatusBadRequest)
		return
	}
	if err := database.UnlinkUserIdentity(h.DB, user.ID, identityUUID); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			http.Error(w, "Identity not found", http.StatusNotFound)
			return
		}
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusOK)
}
