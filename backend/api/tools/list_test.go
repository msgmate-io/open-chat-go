package tools

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

// The chat-start and bot-edit screens fetch the catalog with page_size=400 to
// discover which selected tools require init values. The page size must not be
// silently reduced to the default (12), otherwise tools such as
// kubernetes_select_cluster never appear in the response and their selection
// widgets are not rendered.
func TestListAcceptsLargePageSizeForCatalogBootstrap(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/tools?page=1&page_size=400", nil)
	rec := httptest.NewRecorder()

	h := &ToolsHandler{}
	h.List(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var resp ToolsListResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("failed decoding response: %v", err)
	}
	if resp.PageSize != 400 {
		t.Fatalf("page_size = %d, want 400 (must not fall back to the default page size)", resp.PageSize)
	}
}
