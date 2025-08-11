package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"backend/database"

	"gorm.io/gorm"
)

// setupBadgeRoutingDB mirrors the chats package badge fixture but runs inside
// the server package so the routing table itself can be exercised.
func setupBadgeRoutingDB(t *testing.T) (*gorm.DB, string) {
	t.Helper()
	config := setupServerTestDB(t)
	DB := database.SetupDatabase(*config)

	owner := createAutomatedUser(t, DB, "owner@example.com", false)
	botUser := createAutomatedUser(t, DB, "bot@example.com", false)

	chat := database.Chat{
		User1Id:  owner.ID,
		User2Id:  botUser.ID,
		ChatType: "interaction",
	}
	if err := DB.Create(&chat).Error; err != nil {
		t.Fatalf("failed to create chat: %v", err)
	}

	share := database.SharedChatInstance{
		ChatId:        chat.ID,
		OwningUserId:  owner.ID,
		ChatShareUUID: "share-" + chat.UUID,
	}
	if err := DB.Create(&share).Error; err != nil {
		t.Fatalf("failed to create share: %v", err)
	}

	metaJSON, _ := json.Marshal(map[string]interface{}{"finished": true})
	text := "done"
	message := database.Message{
		ChatId:     chat.ID,
		SenderId:   botUser.ID,
		ReceiverId: owner.ID,
		DataType:   "text",
		Text:       &text,
		MetaData:   metaJSON,
	}
	if err := DB.Create(&message).Error; err != nil {
		t.Fatalf("failed to create message: %v", err)
	}

	return DB, share.ChatShareUUID
}

func TestInteractionBadgeRoutesServeFromAPIAliasAndPublicPath(t *testing.T) {
	DB, shareUUID := setupBadgeRoutingDB(t)

	router, _ := BackendRouting(DB, nil, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}), false, "", "", false)

	paths := []string{
		"/api/interaction/" + shareUUID + "/badge.svg",
		"/interaction/" + shareUUID + "/badge.svg",
	}

	for _, path := range paths {
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.Host = "badge.example.com"
		router.ServeHTTP(recorder, req)

		body := recorder.Body.String()
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s: expected 200, got %d: %s", path, recorder.Code, body)
		}
		if ct := recorder.Header().Get("Content-Type"); !strings.Contains(ct, "image/svg+xml") {
			t.Fatalf("%s: expected image/svg+xml content type, got %q", path, ct)
		}
		if !strings.Contains(body, "<svg") || !strings.Contains(body, "</svg>") {
			t.Fatalf("%s: expected valid SVG, got: %s", path, body)
		}
		if !strings.Contains(body, ">finished</text>") {
			t.Fatalf("%s: expected finished state text in badge, got: %s", path, body)
		}
		if !strings.Contains(body, "badge.example.com") {
			t.Fatalf("%s: expected server host in detailed badge, got: %s", path, body)
		}
	}
}

func TestInteractionBadgeAliasUnknownShareRendersNotFoundSVG(t *testing.T) {
	config := setupServerTestDB(t)
	DB := database.SetupDatabase(*config)

	router, _ := BackendRouting(DB, nil, nil, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}), false, "", "", false)

	recorder := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/interaction/does-not-exist/badge.svg", nil)
	router.ServeHTTP(recorder, req)

	body := recorder.Body.String()
	if recorder.Code != http.StatusNotFound {
		t.Fatalf("expected 404, got %d: %s", recorder.Code, body)
	}
	if !strings.Contains(body, ">not found</text>") {
		t.Fatalf("expected not found state text in badge, got: %s", body)
	}
}
