package user

import (
	"backend/api/admin"
	"backend/database"
	"backend/server/util"
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"gorm.io/gorm"
)

func setupRegisterTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	cfg := database.DBConfig{
		Backend:  "sqlite",
		FilePath: filepath.Join(t.TempDir(), "register_test.db"),
		Debug:    false,
		ResetDB:  true,
	}
	return database.SetupDatabase(cfg)
}

func TestRegisterCreatesUserWhenApprovalDisabled(t *testing.T) {
	DB := setupRegisterTestDB(t)
	h := &UserHandler{SignupRequiresAdminApproval: false}

	body := []byte(`{"name":"Test User","email":"user@example.com","password":"Passw0rd!"}`)
	req := httptest.NewRequest("POST", "/api/v1/user/register", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), "db", DB))

	rr := httptest.NewRecorder()
	h.Register(rr, req)

	if rr.Code != 201 {
		t.Fatalf("expected status 201, got %d: %s", rr.Code, rr.Body.String())
	}

	var createdUser database.User
	if err := DB.First(&createdUser, "email = ?", "user@example.com").Error; err != nil {
		t.Fatalf("expected user to be created: %v", err)
	}

	if createdUser.PasswordHash == "" {
		t.Fatalf("expected password hash to be set")
	}

	var pendingRequests int64
	if err := DB.Model(&database.RegistrationRequest{}).Count(&pendingRequests).Error; err != nil {
		t.Fatalf("failed to count registration requests: %v", err)
	}
	if pendingRequests != 0 {
		t.Fatalf("expected no registration requests, got %d", pendingRequests)
	}
}

func TestRegisterCreatesPendingRequestWhenApprovalEnabled(t *testing.T) {
	DB := setupRegisterTestDB(t)
	h := &UserHandler{SignupRequiresAdminApproval: true}

	body := []byte(`{"name":"Pending User","email":"pending@example.com","password":"Passw0rd!"}`)
	req := httptest.NewRequest("POST", "/api/v1/user/register", bytes.NewReader(body))
	req = req.WithContext(context.WithValue(req.Context(), "db", DB))

	rr := httptest.NewRecorder()
	h.Register(rr, req)

	if rr.Code != 202 {
		t.Fatalf("expected status 202, got %d: %s", rr.Code, rr.Body.String())
	}

	var createdUser database.User
	err := DB.First(&createdUser, "email = ?", "pending@example.com").Error
	if err == nil {
		t.Fatalf("expected user to not be created before admin approval")
	}
	if err != gorm.ErrRecordNotFound {
		t.Fatalf("unexpected error looking up user: %v", err)
	}

	var reqModel database.RegistrationRequest
	if err := DB.First(&reqModel, "email = ?", "pending@example.com").Error; err != nil {
		t.Fatalf("expected registration request to be created: %v", err)
	}
	if reqModel.Status != database.RegistrationRequestStatusPending {
		t.Fatalf("expected pending status, got %s", reqModel.Status)
	}
	if reqModel.PasswordHash == "" {
		t.Fatalf("expected password hash in registration request")
	}
}

func TestApproveRegistrationRequestCreatesUser(t *testing.T) {
	DB := setupRegisterTestDB(t)
	h := &UserHandler{SignupRequiresAdminApproval: true}

	registerBody := []byte(`{"name":"Approve Me","email":"approve@example.com","password":"Passw0rd!"}`)
	registerReq := httptest.NewRequest("POST", "/api/v1/user/register", bytes.NewReader(registerBody))
	registerReq = registerReq.WithContext(context.WithValue(registerReq.Context(), "db", DB))

	registerRR := httptest.NewRecorder()
	h.Register(registerRR, registerReq)
	if registerRR.Code != 202 {
		t.Fatalf("expected register status 202, got %d: %s", registerRR.Code, registerRR.Body.String())
	}

	var registerResp map[string]string
	if err := json.Unmarshal(registerRR.Body.Bytes(), &registerResp); err != nil {
		t.Fatalf("failed to decode registration response: %v", err)
	}
	requestUUID := registerResp["request_uuid"]
	if requestUUID == "" {
		t.Fatalf("expected request_uuid in response")
	}

	err, adminUser := util.CreateUser(DB, "admin@example.com", "AdminPass1!", true)
	if err != nil {
		t.Fatalf("failed to create admin user: %v", err)
	}

	approveReq := httptest.NewRequest("POST", "/api/v1/admin/registration-requests/"+requestUUID+"/approve", bytes.NewReader([]byte(`{"review_note":"looks good"}`)))
	approveReq.SetPathValue("request_uuid", requestUUID)
	ctx := context.WithValue(approveReq.Context(), "db", DB)
	ctx = context.WithValue(ctx, "user", adminUser)
	approveReq = approveReq.WithContext(ctx)

	approveRR := httptest.NewRecorder()
	admin.ApproveRegistrationRequest(approveRR, approveReq)

	if approveRR.Code != 200 {
		t.Fatalf("expected approve status 200, got %d: %s", approveRR.Code, approveRR.Body.String())
	}

	var reqModel database.RegistrationRequest
	if err := DB.First(&reqModel, "uuid = ?", requestUUID).Error; err != nil {
		t.Fatalf("failed to fetch updated registration request: %v", err)
	}
	if reqModel.Status != database.RegistrationRequestStatusApproved {
		t.Fatalf("expected approved status, got %s", reqModel.Status)
	}
	if reqModel.ApprovedUserId == nil {
		t.Fatalf("expected approved user id to be set")
	}

	var createdUser database.User
	if err := DB.First(&createdUser, "email = ?", "approve@example.com").Error; err != nil {
		t.Fatalf("expected approved user to be created: %v", err)
	}
}
