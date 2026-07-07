package admin

import (
	"backend/database"
	"backend/runtimecfg"
	"backend/server/util"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
)

func requireEmailVerification() bool {
	v, ok := runtimecfg.GetAll()[database.RequireEmailVerificationEnvKey]
	if !ok {
		return true
	}
	required, err := strconv.ParseBool(strings.TrimSpace(v.Value))
	if err != nil {
		return true
	}
	return required
}

type RegistrationDecisionPayload struct {
	ReviewNote string `json:"review_note"`
}

type RegistrationRequestsResponse struct {
	Requests []database.RegistrationRequest `json:"requests"`
}

func GetRegistrationRequests(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	if !user.IsAdmin {
		http.Error(w, "User is not an admin", http.StatusForbidden)
		return
	}

	query := DB.Model(&database.RegistrationRequest{}).
		Preload("ReviewedByUser").
		Preload("ApprovedUser").
		Order("created_at DESC")

	status := strings.TrimSpace(strings.ToLower(r.URL.Query().Get("status")))
	if status != "" {
		if status != database.RegistrationRequestStatusPending && status != database.RegistrationRequestStatusApproved && status != database.RegistrationRequestStatusRejected {
			http.Error(w, "Invalid status", http.StatusBadRequest)
			return
		}
		query = query.Where("status = ?", status)
	}

	var requests []database.RegistrationRequest
	if err := query.Find(&requests).Error; err != nil {
		http.Error(w, "Unable to fetch registration requests", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(RegistrationRequestsResponse{Requests: requests})
}

func ApproveRegistrationRequest(w http.ResponseWriter, r *http.Request) {
	decisionRegistrationRequest(w, r, database.RegistrationRequestStatusApproved)
}

func RejectRegistrationRequest(w http.ResponseWriter, r *http.Request) {
	decisionRegistrationRequest(w, r, database.RegistrationRequestStatusRejected)
}

func decisionRegistrationRequest(w http.ResponseWriter, r *http.Request, targetStatus string) {
	DB, user, err := util.GetDBAndUser(r)
	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	if !user.IsAdmin {
		http.Error(w, "User is not an admin", http.StatusForbidden)
		return
	}

	requestUUID := strings.TrimSpace(r.PathValue("request_uuid"))
	if requestUUID == "" {
		http.Error(w, "request_uuid is required", http.StatusBadRequest)
		return
	}

	payload := RegistrationDecisionPayload{}
	if r.Body != nil {
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil && !errors.Is(err, io.EOF) {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
	}
	payload.ReviewNote = strings.TrimSpace(payload.ReviewNote)

	now := time.Now()
	var updatedRequest database.RegistrationRequest

	err = DB.Transaction(func(tx *gorm.DB) error {
		var regRequest database.RegistrationRequest
		if err := tx.First(&regRequest, "uuid = ?", requestUUID).Error; err != nil {
			return err
		}

		if regRequest.Status != database.RegistrationRequestStatusPending {
			return gorm.ErrInvalidData
		}

		updates := map[string]interface{}{
			"status":              targetStatus,
			"review_note":         payload.ReviewNote,
			"reviewed_at":         &now,
			"reviewed_by_user_id": user.ID,
		}

		if targetStatus == database.RegistrationRequestStatusApproved {
			var existingUser database.User
			if err := tx.First(&existingUser, "email = ?", regRequest.Email).Error; err == nil {
				return gorm.ErrDuplicatedKey
			} else if err != nil && err != gorm.ErrRecordNotFound {
				return err
			}

			newUser := database.User{
				Name:         regRequest.Name,
				Email:        regRequest.Email,
				PasswordHash: regRequest.PasswordHash,
				IsAdmin:      false,
			}
			if err := tx.Create(&newUser).Error; err != nil {
				return err
			}
			if requireEmailVerification() {
				if err := database.SetUserEmailVerified(tx, newUser.ID, false); err != nil {
					return err
				}
			}

			updates["approved_user_id"] = newUser.ID
		}

		if err := tx.Model(&regRequest).Updates(updates).Error; err != nil {
			return err
		}

		if err := tx.Preload("ReviewedByUser").Preload("ApprovedUser").First(&updatedRequest, "id = ?", regRequest.ID).Error; err != nil {
			return err
		}

		return nil
	})

	if err != nil {
		switch err {
		case gorm.ErrRecordNotFound:
			http.Error(w, "Registration request not found", http.StatusNotFound)
			return
		case gorm.ErrInvalidData:
			http.Error(w, "Registration request has already been reviewed", http.StatusBadRequest)
			return
		case gorm.ErrDuplicatedKey:
			http.Error(w, "A user with this email already exists", http.StatusConflict)
			return
		default:
			http.Error(w, "Unable to update registration request", http.StatusInternalServerError)
			return
		}
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(updatedRequest)
}
