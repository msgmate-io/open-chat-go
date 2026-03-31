package util

import (
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
)

// ErrMissingPathUUID is returned by ParsePathUUID when the named path parameter
// is absent or blank.
var ErrMissingPathUUID = errors.New("missing uuid path parameter")

// ParsePathUUID extracts and validates a UUID path parameter from the request.
//
// Every UUID that open-chat exposes as a path parameter (chat uuid, message
// uuid, ...) is generated canonically (database.Model.BeforeCreate ->
// uuid.New().String()), so a value that does not parse as a UUID can never
// identify a stored row. Handlers used to guard only against an empty value,
// which let malformed input (path fragments, NUL bytes, oversized strings) fall
// through to the database query.
//
// The parsed UUID is returned in canonical lowercase form so the equivalent
// dashed / dashless / upper-case spellings of the same UUID resolve to one
// string before being compared against the stored value.
func ParsePathUUID(r *http.Request, param string) (string, error) {
	raw := strings.TrimSpace(r.PathValue(param))
	if raw == "" {
		return "", ErrMissingPathUUID
	}

	parsed, err := uuid.Parse(raw)
	if err != nil {
		return "", err
	}

	return parsed.String(), nil
}
