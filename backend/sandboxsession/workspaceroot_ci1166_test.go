package sandboxsession

import (
	"errors"
	"strings"
	"testing"
)

// stubProvider satisfies Provider through the embedded interface: only Name is
// implemented because the ci#1166 gate inspects nothing but the capability set.
type stubProvider struct {
	Provider
	name string
}

func (s stubProvider) Name() string { return s.name }

func TestIsCoderWorkspaceRoot(t *testing.T) {
	cases := []struct {
		root string
		want bool
	}{
		{"/workspace/coder-25aaffef-81f6-4568-ab06", true},
		{"/workspace/coder-0012091c-f36b-4b93-89cb", true},
		{"/workspace/", false},
		{"/workspace", false},
		{"/home/user/project", false},
		{"", false},
		{"   ", false},
	}
	for _, c := range cases {
		if got := IsCoderWorkspaceRoot(c.root); got != c.want {
			t.Fatalf("IsCoderWorkspaceRoot(%q) = %v, want %v", c.root, got, c.want)
		}
	}
}

func TestSupportsWorkspaceRootProvisioningWithoutContract(t *testing.T) {
	if SupportsWorkspaceRootProvisioning(stubProvider{name: "msgmate_coder"}) {
		t.Fatal("a provider that embeds Provider has no materialisation contract")
	}
}

// ci#1166: the coder root must never be advertised to a tool call by a provider
// that cannot materialise it. This is the fail-open hole that survived ci#1126.
func TestAssertWorkspaceRootCapabilityFailsClosedForCoderRoot(t *testing.T) {
	const root = "/workspace/coder-25aaffef-81f6-4568-ab06"

	err := AssertWorkspaceRootCapability(stubProvider{name: "msgmate_coder"}, root)
	if err == nil {
		t.Fatal("expected an error for a coder root without the materialisation contract")
	}
	if !errors.Is(err, ErrUnsupportedOperation) {
		t.Fatalf("error %v is not classified as ErrUnsupportedOperation", err)
	}
	if !strings.Contains(err.Error(), root) {
		t.Fatalf("error %v does not name the workspace root %q", err, root)
	}
	if !strings.Contains(err.Error(), "ci#1166") {
		t.Fatalf("error %v does not cite the ticket", err)
	}
}

func TestAssertWorkspaceRootCapabilityKeepsOtherRootsFailOpen(t *testing.T) {
	provider := stubProvider{name: "docker"}
	for _, root := range []string{"/workspace", "/home/user/project", ""} {
		if err := AssertWorkspaceRootCapability(provider, root); err != nil {
			t.Fatalf("root %q must keep the pre-existing fail-open behaviour, got %v", root, err)
		}
	}
	if err := AssertWorkspaceRootCapability(nil, "/workspace/coder-x"); err != nil {
		t.Fatalf("a nil provider must not fail the gate, got %v", err)
	}
}
