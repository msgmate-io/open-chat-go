package msgmate

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	client "github.com/msgmate-io/go-client-integration/goclient"
)

// newFileHandlerTestServer serves a single uploaded file payload at
// /api/v1/files/<id> with the given content type.
func newFileHandlerTestServer(t *testing.T, fileID, contentType, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v1/files/"+fileID {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", contentType)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestProcessAttachmentsInlinesTextForNonOpenAIBackend(t *testing.T) {
	const marker = "OPENCHAT-FILE-OK-12345"
	srv := newFileHandlerTestServer(t, "text-file-1", "text/plain", marker)

	fh := NewFileHandler(&BotContext{Client: client.NewClient(srv.URL)})
	attachments := []interface{}{
		map[string]interface{}{"file_id": "text-file-1", "mime_type": "text/plain"},
	}

	contentArray, err := fh.ProcessAttachments(attachments, "litellm")
	if err != nil {
		t.Fatalf("ProcessAttachments returned error: %v", err)
	}
	if len(contentArray) != 1 {
		t.Fatalf("expected 1 content entry, got %d: %#v", len(contentArray), contentArray)
	}
	if contentArray[0]["type"] != "text" {
		t.Fatalf("expected text content type, got %#v", contentArray[0])
	}
	text, _ := contentArray[0]["text"].(string)
	if !strings.Contains(text, marker) {
		t.Fatalf("expected inlined text to contain %q, got %q", marker, text)
	}
}

func TestProcessAttachmentsBuildsImageURLData(t *testing.T) {
	srv := newFileHandlerTestServer(t, "image-file-1", "image/jpeg", "\xff\xd8\xff")

	fh := NewFileHandler(&BotContext{Client: client.NewClient(srv.URL)})
	attachments := []interface{}{
		map[string]interface{}{"file_id": "image-file-1", "mime_type": "image/jpeg"},
	}

	contentArray, err := fh.ProcessAttachments(attachments, "litellm")
	if err != nil {
		t.Fatalf("ProcessAttachments returned error: %v", err)
	}
	if len(contentArray) != 1 {
		t.Fatalf("expected 1 content entry, got %d: %#v", len(contentArray), contentArray)
	}
	if contentArray[0]["type"] != "image_url" {
		t.Fatalf("expected image_url content type, got %#v", contentArray[0])
	}
	imageURL, _ := contentArray[0]["image_url"].(map[string]interface{})
	url, _ := imageURL["url"].(string)
	if !strings.HasPrefix(url, "data:image/jpeg;base64,") {
		t.Fatalf("expected a base64 image data URL, got %q", url)
	}
}

func TestProcessAttachmentsSkipsLargeTextForNonOpenAIBackend(t *testing.T) {
	large := strings.Repeat("a", maxInlineTextAttachmentBytes+1)
	srv := newFileHandlerTestServer(t, "text-file-large", "text/plain", large)

	fh := NewFileHandler(&BotContext{Client: client.NewClient(srv.URL)})
	attachments := []interface{}{
		map[string]interface{}{"file_id": "text-file-large", "mime_type": "text/plain"},
	}

	contentArray, err := fh.ProcessAttachments(attachments, "litellm")
	if err != nil {
		t.Fatalf("ProcessAttachments returned error: %v", err)
	}
	if len(contentArray) != 0 {
		t.Fatalf("expected oversized text attachment to be skipped, got %#v", contentArray)
	}
}
