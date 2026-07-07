package user

import (
	"backend/database"
	"backend/runtimecfg"
	"encoding/json"
	"errors"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
	"io"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
)

var (
	ErrEmailAlreadyInUse                 = errors.New("email already in use")
	ErrRegistrationRequestAlreadyPending = errors.New("registration request already pending")
)

type RegistrationResult struct {
	User                *database.User
	RegistrationRequest *database.RegistrationRequest
}

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

func SignupRequiresAdminApprovalFromRuntimeConfig() bool {
	return signupRequiresAdminApprovalFromRuntimeConfig()
}

func CreateUserOrRegistrationRequest(DB *gorm.DB, name string, email string, passwordHash string, signupRequiresAdminApproval bool) (RegistrationResult, error) {
	name = strings.TrimSpace(name)
	email = strings.TrimSpace(strings.ToLower(email))
	passwordHash = strings.TrimSpace(passwordHash)

	if name == "" {
		return RegistrationResult{}, errors.New("name is required")
	}
	if email == "" {
		return RegistrationResult{}, errors.New("email is required")
	}
	if passwordHash == "" {
		return RegistrationResult{}, errors.New("password hash is required")
	}

	if _, err := mail.ParseAddress(email); err != nil {
		return RegistrationResult{}, errors.New("invalid email")
	}

	var existingUser database.User
	if err := DB.First(&existingUser, "email = ?", email).Error; err == nil {
		return RegistrationResult{}, ErrEmailAlreadyInUse
	} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return RegistrationResult{}, err
	}

	if signupRequiresAdminApproval {
		var existingRequest database.RegistrationRequest
		err := DB.First(&existingRequest, "email = ? AND status = ?", email, database.RegistrationRequestStatusPending).Error
		if err == nil {
			return RegistrationResult{}, ErrRegistrationRequestAlreadyPending
		}
		if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
			return RegistrationResult{}, err
		}

		registrationRequest := &database.RegistrationRequest{
			Name:         name,
			Email:        email,
			PasswordHash: passwordHash,
			Status:       database.RegistrationRequestStatusPending,
		}

		if err := DB.Create(registrationRequest).Error; err != nil {
			return RegistrationResult{}, err
		}

		return RegistrationResult{RegistrationRequest: registrationRequest}, nil
	}

	createdUser := &database.User{
		Name:         name,
		Email:        email,
		PasswordHash: passwordHash,
		ContactToken: uuid.New().String(),
		IsAdmin:      false,
	}

	if err := DB.Create(createdUser).Error; err != nil {
		return RegistrationResult{}, err
	}

	return RegistrationResult{User: createdUser}, nil
}

func registerWithAdminApproval(w http.ResponseWriter, r *http.Request, signupRequiresAdminApproval bool) {
	var data UserRegister

	DB, ok := r.Context().Value("db").(*gorm.DB)
	if !ok {
		http.Error(w, "Unable to get database", http.StatusBadRequest)
		return
	}

	contentType := strings.ToLower(strings.TrimSpace(r.Header.Get("Content-Type")))
	bodyBytes, err := io.ReadAll(io.LimitReader(r.Body, 1024*1024))
	if err != nil {
		http.Error(w, "Unable to read request body", http.StatusBadRequest)
		return
	}
	bodyText := strings.TrimSpace(string(bodyBytes))
	if strings.Contains(contentType, "application/json") || strings.HasPrefix(bodyText, "{") {
		if err := json.Unmarshal(bodyBytes, &data); err != nil {
			http.Error(w, "Invalid JSON", http.StatusBadRequest)
			return
		}
	} else {
		r.Body = io.NopCloser(strings.NewReader(bodyText))
		if err := r.ParseForm(); err != nil {
			http.Error(w, "Invalid form payload", http.StatusBadRequest)
			return
		}
		data.Name = r.FormValue("name")
		data.Email = r.FormValue("email")
		data.Password = r.FormValue("password")
	}

	data.Name = strings.TrimSpace(data.Name)
	data.Email = strings.TrimSpace(strings.ToLower(data.Email))

	if data.Name == "" {
		http.Error(w, "Name is required", http.StatusBadRequest)
		return
	}

	_, err = mail.ParseAddress(data.Email)
	if err != nil {
		http.Error(w, "Invalid email", http.StatusBadRequest)
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

	result, err := CreateUserOrRegistrationRequest(DB, data.Name, data.Email, string(hashedPassword), signupRequiresAdminApproval)
	if err != nil {
		switch {
		case errors.Is(err, ErrEmailAlreadyInUse):
			http.Error(w, "Email already in use", http.StatusBadRequest)
			return
		case errors.Is(err, ErrRegistrationRequestAlreadyPending):
			http.Error(w, "Registration request already pending", http.StatusBadRequest)
			return
		default:
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
	}

	if result.RegistrationRequest != nil {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusAccepted)
		_ = json.NewEncoder(w).Encode(map[string]string{
			"message":      "Registration request submitted and pending admin approval",
			"request_uuid": result.RegistrationRequest.UUID,
			"status":       result.RegistrationRequest.Status,
		})
		return
	}

	w.WriteHeader(http.StatusCreated)
	_, _ = w.Write([]byte("User created"))
}
