package integrationhost

import (
	"testing"

	"github.com/msgmate-io/go-integration-interface/integrationinterface"
)

// TestHostSatisfiesContract pins the stable Host contract (ci#423): the core
// adapter must report the interface module's HostVersion and expose every
// capability surface. A capability added to integrationinterface without a
// matching adapter method already fails the compile-time assertion in host.go;
// this test guards the runtime shape (no nil surface, matching version) so the
// contract cannot silently degrade for integrations that gate on HostVersion.
func TestHostSatisfiesContract(t *testing.T) {
	t.Cleanup(func() { integrationinterface.SetHost(nil) })
	Install()

	h := integrationinterface.RequireHost()
	if got, want := h.Version(), integrationinterface.HostVersion; got != want {
		t.Fatalf("host version = %d, want %d", got, want)
	}

	surfaces := map[string]interface{}{
		"Config":      h.Config(),
		"Logger":      h.Logger(),
		"HTTP":        h.HTTP(),
		"Storage":     h.Storage(),
		"Chat":        h.Chat(),
		"Workspace":   h.Workspace(),
		"Web":         h.Web(),
		"Tokens":      h.Tokens(),
		"Tools":       h.Tools(),
		"ConfigWrite": h.ConfigWrite(),
		"Users":       h.Users(),
	}
	for name, surface := range surfaces {
		if surface == nil {
			t.Errorf("host surface %s is nil", name)
		}
	}

	// Storage().Handle() is the opaque persistence handle integrations pass to
	// their ORM helpers; it must be the core's *gorm.DB and must never be typed
	// into the interface module itself.
	if handle := h.Storage().Handle(); handle == nil {
		t.Error("Storage().Handle() is nil; the opaque persistence handle must be exposed")
	}
}
