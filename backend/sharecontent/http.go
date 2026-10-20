package sharecontent

import (
	"encoding/json"
	"net/http"
)

// WriteRedactedJSON marshals the payload, strips everything the share content
// mode forbids and writes the safe result to the client.
//
// Redaction is applied to the serialized form on purpose: every field that is
// ever added to a public response type is therefore redacted by default instead
// of leaking because somebody forgot to update a struct literal.
func WriteRedactedJSON(w http.ResponseWriter, payload any, mode Mode) error {
	raw, err := json.Marshal(payload)
	if err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return err
	}

	var decoded any
	if err = json.Unmarshal(raw, &decoded); err != nil {
		http.Error(w, "Failed to encode response", http.StatusInternalServerError)
		return err
	}

	redacted := RedactValue(decoded, NormalizeMode(string(mode)), 0)

	w.Header().Set("Content-Type", "application/json")
	return json.NewEncoder(w).Encode(redacted)
}
