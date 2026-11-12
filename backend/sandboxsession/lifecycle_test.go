package sandboxsession

import (
	"context"
	"errors"
	"testing"
	"time"
)

// lifecycleProvider is a fakeProvider that additionally implements the optional
// lifecycle capabilities (ci#1060): attach, detach and destroy.
type lifecycleProvider struct {
	fakeProvider

	attachCalls  int
	detachCalls  int
	destroyCalls int

	gotAttachReq Request
	gotRef       SandboxRef

	attachErr  error
	detachErr  error
	destroyErr error
}

func (f *lifecycleProvider) AttachSandbox(_ context.Context, req Request) (Session, error) {
	f.attachCalls++
	f.gotAttachReq = req
	if f.attachErr != nil {
		return Session{}, f.attachErr
	}
	return Session{
		SandboxUUID: req.SandboxUUID,
		SessionID:   "sess-attached",
		AttachPath:  "/terminal/" + req.SandboxUUID,
		AttachToken: "attach-token",
		ExpiresAt:   time.Now().Add(time.Hour),
	}, nil
}

func (f *lifecycleProvider) CloseSession(_ context.Context, _ Session) error {
	f.detachCalls++
	return f.detachErr
}

func (f *lifecycleProvider) DestroySandbox(_ context.Context, ref SandboxRef) error {
	f.destroyCalls++
	f.gotRef = ref
	return f.destroyErr
}

// plainProvider implements only the base Provider, so the lifecycle helpers must
// report ErrUnsupportedOperation instead of guessing.
type plainProvider struct{ fakeProvider }

func TestNormalizeRejectsTemplateAndSandboxUUID(t *testing.T) {
	req := Request{Template: "coder", SandboxUUID: "sbx-1"}
	err := Normalize(&req)
	if !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("Normalize() error = %v, want ErrInvalidRequest", err)
	}
}

func TestNormalizeTrimsSandboxUUID(t *testing.T) {
	req := Request{SandboxUUID: "  sbx-1  "}
	if err := Normalize(&req); err != nil {
		t.Fatalf("Normalize() error = %v", err)
	}
	if req.SandboxUUID != "sbx-1" {
		t.Fatalf("SandboxUUID = %q, want %q", req.SandboxUUID, "sbx-1")
	}
}

func TestOpenRejectsTemplateWithSandboxUUID(t *testing.T) {
	p := &lifecycleProvider{fakeProvider: fakeProvider{name: "fake"}}
	reg := NewRegistry()
	if err := reg.Register(p); err != nil {
		t.Fatalf("register: %v", err)
	}

	if _, err := Open(context.Background(), reg, Request{Template: "coder", SandboxUUID: "sbx-1"}); err == nil {
		t.Fatal("Open() with template+sandbox_uuid should fail")
	}
	if p.ensureCalls != 0 {
		t.Fatalf("EnsureSandbox called %d times, want 0", p.ensureCalls)
	}
}

func TestAttachSandboxNeverProvisions(t *testing.T) {
	p := &lifecycleProvider{fakeProvider: fakeProvider{name: "fake"}}
	reg := NewRegistry()
	if err := reg.Register(p); err != nil {
		t.Fatalf("register: %v", err)
	}

	session, err := AttachSandbox(context.Background(), reg, Request{SandboxUUID: "sbx-7", OwnerUUID: "user-1"})
	if err != nil {
		t.Fatalf("AttachSandbox() error = %v", err)
	}
	if p.ensureCalls != 0 {
		t.Fatalf("EnsureSandbox called %d times on attach, want 0", p.ensureCalls)
	}
	if p.attachCalls != 1 {
		t.Fatalf("AttachSandbox called %d times, want 1", p.attachCalls)
	}
	if session.Provider != "fake" {
		t.Fatalf("session.Provider = %q, want %q", session.Provider, "fake")
	}
	if session.SandboxUUID != "sbx-7" {
		t.Fatalf("session.SandboxUUID = %q, want %q", session.SandboxUUID, "sbx-7")
	}
	if session.SessionID != "sess-attached" {
		t.Fatalf("session.SessionID = %q", session.SessionID)
	}
}

func TestAttachSandboxRequiresSandboxUUID(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(&lifecycleProvider{fakeProvider: fakeProvider{name: "fake"}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := AttachSandbox(context.Background(), reg, Request{}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("AttachSandbox() error = %v, want ErrInvalidRequest", err)
	}
}

func TestAttachSandboxRejectsTemplate(t *testing.T) {
	p := &lifecycleProvider{fakeProvider: fakeProvider{name: "fake"}}
	reg := NewRegistry()
	if err := reg.Register(p); err != nil {
		t.Fatalf("register: %v", err)
	}
	if _, err := AttachSandbox(context.Background(), reg, Request{SandboxUUID: "sbx-1", Template: "coder"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("AttachSandbox() error = %v, want ErrInvalidRequest", err)
	}
	if p.attachCalls != 0 {
		t.Fatalf("provider AttachSandbox called %d times, want 0", p.attachCalls)
	}
}

func TestCloseSessionDetachesWithoutDestroying(t *testing.T) {
	p := &lifecycleProvider{fakeProvider: fakeProvider{name: "fake"}}
	reg := NewRegistry()
	if err := reg.Register(p); err != nil {
		t.Fatalf("register: %v", err)
	}

	session := Session{Provider: "fake", SandboxUUID: "sbx-1", SessionID: "sess-1"}
	if err := CloseSession(context.Background(), reg, session); err != nil {
		t.Fatalf("CloseSession() error = %v", err)
	}
	if p.detachCalls != 1 {
		t.Fatalf("CloseSession called %d times, want 1", p.detachCalls)
	}
	if p.destroyCalls != 0 {
		t.Fatalf("DestroySandbox called %d times on detach, want 0", p.destroyCalls)
	}
}

func TestCloseSessionRequiresSessionID(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(&lifecycleProvider{fakeProvider: fakeProvider{name: "fake"}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := CloseSession(context.Background(), reg, Session{Provider: "fake"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("CloseSession() error = %v, want ErrInvalidRequest", err)
	}
}

func TestDestroySandboxRequiresSandboxUUID(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(&lifecycleProvider{fakeProvider: fakeProvider{name: "fake"}}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := DestroySandbox(context.Background(), reg, SandboxRef{Provider: "fake"}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("DestroySandbox() error = %v, want ErrInvalidRequest", err)
	}
}

func TestDestroySandboxCallsProvider(t *testing.T) {
	p := &lifecycleProvider{fakeProvider: fakeProvider{name: "fake"}}
	reg := NewRegistry()
	if err := reg.Register(p); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := DestroySandbox(context.Background(), reg, SandboxRef{Provider: "fake", SandboxUUID: "sbx-1"}); err != nil {
		t.Fatalf("DestroySandbox() error = %v", err)
	}
	if p.destroyCalls != 1 {
		t.Fatalf("DestroySandbox called %d times, want 1", p.destroyCalls)
	}
	if p.gotRef.SandboxUUID != "sbx-1" {
		t.Fatalf("destroy ref SandboxUUID = %q", p.gotRef.SandboxUUID)
	}
}

func TestUnspportedLifecycleCapabilities(t *testing.T) {
	p := &plainProvider{fakeProvider: fakeProvider{name: "plain"}}
	reg := NewRegistry()
	if err := reg.Register(p); err != nil {
		t.Fatalf("register: %v", err)
	}

	if SupportsAttach(p) || SupportsDetach(p) || SupportsDestroy(p) {
		t.Fatal("plain provider must not report any lifecycle capability")
	}
	if _, err := AttachSandbox(context.Background(), reg, Request{SandboxUUID: "sbx-1"}); !errors.Is(err, ErrUnsupportedOperation) {
		t.Fatalf("AttachSandbox() error = %v, want ErrUnsupportedOperation", err)
	}
	if err := CloseSession(context.Background(), reg, Session{Provider: "plain", SessionID: "sess-1"}); !errors.Is(err, ErrUnsupportedOperation) {
		t.Fatalf("CloseSession() error = %v, want ErrUnsupportedOperation", err)
	}
	if err := DestroySandbox(context.Background(), reg, SandboxRef{Provider: "plain", SandboxUUID: "sbx-1"}); !errors.Is(err, ErrUnsupportedOperation) {
		t.Fatalf("DestroySandbox() error = %v, want ErrUnsupportedOperation", err)
	}
}
