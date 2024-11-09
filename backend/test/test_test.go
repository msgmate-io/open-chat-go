package test

import (
	"backend/api"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHelloHandler(t *testing.T) {
	mux := api.BackendRouting()
	ts := httptest.NewServer(mux)
	defer ts.Close()

	res, err := http.Get(ts.URL + "/api/v1/test")
	if err != nil {
		t.Fatal(err)
	}

	fmt.Println(res)
}
