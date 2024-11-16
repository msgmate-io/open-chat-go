package test

import (
	"backend/api"
	"backend/database"
	"backend/server"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// go test -v ./... -run "^TestUserApis$"
func TestUserApis(t *testing.T) {

	database.SetupDatabase("sqlite", "data.db", false)
	s, _ := server.BackendServer("localhost", 1984, false, false)

	ts := httptest.NewServer(s.Handler)
	defer ts.Close()

	// Test registering a user
	userRegister := api.UserRegister{
		Name:     "test",
		Email:    "test@mail.com",
		Password: "test",
	}

	b, err := json.Marshal(userRegister)
	if err != nil {
		t.Fatal(err)
	}

	res, err := http.Post(ts.URL+"/api/v1/user/register", "application/json", strings.NewReader(string(b)))
	if err != nil {
		t.Fatal(err)
	}

	if res.StatusCode != http.StatusOK {
		t.Fatalf("Expected status code 200, got %d", res.StatusCode)
	}

	//Register the smae user again
	res, err = http.Post(ts.URL+"/api/v1/user/register", "application/json", strings.NewReader(string(b)))

	if err != nil {
		t.Fatal(err)
	}

	if res.StatusCode != http.StatusBadRequest {
		t.Fatalf("Expected status code 400, got %d", res.StatusCode)
	}

}
