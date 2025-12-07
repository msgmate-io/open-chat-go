// Package secretstore is the shared two-way seal/unseal abstraction used for
// sensitive values that must not be stored in plain text. It underpins two
// homes for sealed data:
//
//   - config blocks embedded in the open-chat config document (resolved at
//     boot, before the database exists), and
//   - per-user blocks persisted by integrations and referenced by a stable ref
//     string.
//
// A Block is a self-describing, opaque, versioned envelope. Callers pass a
// Block around instead of the plaintext; the Algorithm field selects the
// Provider that can unseal it. Providers register themselves through
// RegisterProvider so this package never needs to import an integration.
package secretstore

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
)

// Algorithm identifiers. The algorithm determines the provider and the envelope
// layout of a Block.
const (
	// AlgoTVaultV1 is wire compatible with the Python/CLI tvault tooling
	// (SHA256(password) key, AES-256-CBC, IV||ciphertext, PKCS#7).
	AlgoTVaultV1 = "tvault-aes256cbc-v1"
	// AlgoLocalV1 is the per-integration AES-256-GCM scheme historically
	// copy-pasted into the git/kubernetes/matrix/signal integrations.
	AlgoLocalV1 = "local-aes256gcm-v1"
	// AlgoTVaultV2 is the recommended default for new blocks: Argon2id KDF +
	// AES-256-GCM.
	AlgoTVaultV2 = "tvault-argon2id-aes256gcm-v1"
)

var (
	// ErrAuthorizationRequired is returned by Unseal when the provider is
	// active but the caller has no valid authorization for the block owner.
	ErrAuthorizationRequired = errors.New("secretstore: authorization required")
	// ErrNoProvider is returned when no secret provider is active.
	ErrNoProvider = errors.New("secretstore: no secret provider is active")
	// ErrUnknownAlgorithm is returned when no provider supports a block's
	// algorithm.
	ErrUnknownAlgorithm = errors.New("secretstore: unknown block algorithm")
	// ErrBlockRefRequired is returned when a block is sealed without a ref.
	ErrBlockRefRequired = errors.New("secretstore: block ref is required")
	// ErrBlockDataRequired is returned when an unseal is attempted on a block
	// that carries no ciphertext.
	ErrBlockDataRequired = errors.New("secretstore: block data is required")
)

// Block is the opaque unit of sealed data. Ref is the public handle; Data is the
// only secret-bearing field. OwnerUUID is empty for server-global config blocks.
type Block struct {
	Ref       string `json:"ref,omitempty"`
	OwnerUUID string `json:"owner,omitempty"`
	Scope     string `json:"scope,omitempty"`
	Label     string `json:"label,omitempty"`
	Algorithm string `json:"algorithm,omitempty"`
	// Data holds the ciphertext. It is base64 encoded in JSON/YAML.
	Data      []byte `json:"data,omitempty"`
	CreatedAt int64  `json:"created_at,omitempty"`
}

// NewRef returns a fresh random block reference (16 random bytes, hex encoded).
func NewRef() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}
