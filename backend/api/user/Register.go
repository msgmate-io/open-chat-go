package user

import (
	"backend/database"
	"backend/runtimecfg"
	"encoding/json"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
)

type UserRegister struct {
	Name     string `json:"name"`
	Email    string `json:"email"`
	Password string `json:"password"`
}

// curl -X POST -H "Content-Type: application/json" -H "Origin: localhost:1984" -d '{"name": "Tim Here", "email":"tim+test@timschupp.de","password":"password"}' http://localhost:1984/api/v1/user/register -v

// Register a user
//
//	@Summary      Register a user
//	@Description  Register a user
//	@Tags         accounts
//	@Accept       json
//	@Produce      json
//	@Param        request body UserRegister true "User registration request"
//	@Success      201  {string}  string	"User created"
//	@Failure      400  {string}  string	"Invalid email"
//	@Failure      400  {string}  string	"Email already in use"
//	@Failure      400  {string}  string	"Password too short"
//	@Failure      500  {string}  string	"Internal server error"
//	@Router       /api/v1/user/register [post]
func (h *UserHandler) Register(w http.ResponseWriter, r *http.Request) {
	registerWithAdminApproval(w, r, h.SignupRequiresAdminApproval)
}

func RegisterFromRuntimeConfig(w http.ResponseWriter, r *http.Request) {
	registerWithAdminApproval(w, r, signupRequiresAdminApprovalFromRuntimeConfig())
}

func signupRequiresAdminApprovalFromRuntimeConfig() bool {
	value, ok := runtimecfg.GetAll()["SIGNUP_REQUIRES_ADMIN_APPROVAL"]
	if !ok {
		return false
	}
	required, err := strconv.ParseBool(strings.TrimSpace(value.Value))
	if err != nil {
		return false
	}
	return required
}

func registerWithAdminApproval(w http.ResponseWriter, r *http.Request, signupRequiresAdminApproval bool) {
	var data UserRegister

	DB, ok := r.Context().Value("db").(*gorm.DB)
	if !ok {
		http.Error(w, "Unable to get database", http.StatusBadRequest)
		return
	}

	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	data.Name = strings.TrimSpace(data.Name)
	data.Email = strings.TrimSpace(strings.ToLower(data.Email))

	if data.Name == "" {
		http.Error(w, "Name is required", http.StatusBadRequest)
		return
	}

	_, err := mail.ParseAddress(data.Email)
	if err != nil {
		http.Error(w, "Invalid email", http.StatusBadRequest)
		return
	}

	var user database.User
	q := DB.First(&user, "email = ?", data.Email)

	if q.Error == nil {
		http.Error(w, "Email already in use", http.StatusBadRequest)
		return
	}

	// TODO: check password strength
	if len(data.Password) < 8 {
		http.Error(w, "Password too short", http.StatusBadRequest)
		return
	}

	hashedPassword, err := bcrypt.GenerateFromPassword([]byte(data.Password), bcrypt.DefaultCost)

	if err != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	if signupRequiresAdminApproval {
		var existingRequest database.RegistrationRequest
		requestQuery := DB.First(&existingRequest, "email = ? AND status = ?", data.Email, database.RegistrationRequestStatusPending)
		if requestQuery.Error == nil {
			http.Error(w, "Registration request already pending", http.StatusBadRequest)
			return
		}
		if requestQuery.Error != nil && requestQuery.Error != gorm.ErrRecordNotFound {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		registrationRequest := database.RegistrationRequest{
			Name:         data.Name,
			Email:        data.Email,
			PasswordHash: string(hashedPassword),
			Status:       database.RegistrationRequestStatusPending,
		}

		if err := DB.Create(&registrationRequest).Error; err != nil {
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"message":      "Registration request submitted and pending admin approval",
			"request_uuid": registrationRequest.UUID,
			"status":       registrationRequest.Status,
		})
		return
	}

	user = database.User{
		Name:         data.Name,
		Email:        data.Email,
		PasswordHash: string(hashedPassword),
		ContactToken: uuid.New().String(),
		IsAdmin:      false,
	}

	q = DB.Create(&user)

	if q.Error != nil {
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}

	w.WriteHeader(http.StatusCreated)
	w.Write([]byte("User created"))
}
