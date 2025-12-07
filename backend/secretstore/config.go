package secretstore

import (
	"context"
	"fmt"
	"strings"
)

// ResolveBlocks authorizes the active provider for the boot session and unseals
// every block, returning ref -> plaintext. It is used to resolve the config
// document's `secrets:` section before the database exists.
func ResolveBlocks(ctx context.Context, blocks map[string]Block, password string) (map[string]string, error) {
	if len(blocks) == 0 {
		return nil, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	provider := Active()
	if provider == nil {
		return nil, ErrNoProvider
	}
	if _, err := provider.Authorize(ctx, AuthParams{SessionKey: BootSessionKey, Password: password}); err != nil {
		return nil, err
	}
	out := make(map[string]string, len(blocks))
	for ref, block := range blocks {
		if strings.TrimSpace(block.Ref) == "" {
			block.Ref = ref
		}
		plaintext, err := Unseal(ctx, block)
		if err != nil {
			return nil, fmt.Errorf("failed to unseal secret block %q: %w", ref, err)
		}
		out[ref] = string(plaintext)
	}
	return out, nil
}
