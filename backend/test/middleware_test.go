package test

import (
	"backend/server"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestJsonMiddleware_BadJson(t *testing.T) {
	s, _ := server.BackendServer("localhost", 1984, true, false)

	ts := httptest.NewServer(s.Handler)
	defer ts.Close()

	res, err := http.Post(ts.URL+"/api/v1/test", "application/json", strings.NewReader(`{"name": "test}`))
	if err != nil {
		t.Fatal(err)
	}

	if res.StatusCode != http.StatusBadRequest {
		t.Errorf("Expected status code 400, got %d", res.StatusCode)
	}
}

func TestJsonMiddleware_GoodJson(t *testing.T) {
	s, _ := server.BackendServer("localhost", 1984, true, false)

	ts := httptest.NewServer(s.Handler)
	defer ts.Close()

	res, err := http.Post(ts.URL+"/api/v1/test", "application/json", strings.NewReader(`{"name": "test"}`))
	if err != nil {
		t.Fatal(err)
	}

	if res.StatusCode != http.StatusOK {
		t.Errorf("Expected status code 200, got %d", res.StatusCode)
	}
}
