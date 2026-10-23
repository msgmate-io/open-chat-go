package chats

import (
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// errMissingChatUUID is returned when the {chat_uuid} path parameter is absent
// or blank.
var errMissingChatUUID = errors.New("missing chat uuid")

// parseChatUUID extracts and validates the {chat_uuid} path parameter.
//
// Every chat UUID is generated as a canonical v4 UUID
// (database.Model.BeforeCreate -> uuid.New().String()), so a value that does not
// parse as a UUID can never identify a chat. Validating it here keeps malformed
// input (path-traversal fragments, NUL bytes, oversized strings) out of the
// database queries that previously only rejected an empty value.
//
// Every handler in this package that serves a {chat_uuid} path parameter now
// goes through this helper, so the validation cannot drift between routes.
//
// The parsed UUID is returned in canonical lowercase form, so the equivalent
// dashed/undashed/upper-case spellings of the same UUID resolve to one string
// before it is compared against the stored value.
func parseChatUUID(r *http.Request) (string, error) {
	raw := strings.TrimSpace(r.PathValue("chat_uuid"))
	if raw == "" {
		return "", errMissingChatUUID
	}

	parsed, err := uuid.Parse(raw)
	if err != nil {
		return "", err
	}

	return parsed.String(), nil
}
