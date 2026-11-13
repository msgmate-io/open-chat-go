package sandboxsession

// Detach/destroy lifecycle for continuable sandbox sessions (ci#1060).
//
// The base Provider interface is deliberately NOT extended: a provider that only
// implements EnsureSandbox/InjectFiles/OpenSession (the k8s sandbox provider and
// every existing consumer) keeps compiling unchanged. Re-attaching to an
// existing sandbox, detaching a shell without terminating the sandbox and
// explicitly destroying a sandbox are declared here as optional capability
// interfaces and driven through the package-level helpers below, which report
// ErrUnsupportedOperation when a provider does not implement the capability.
//
// The central invariant: DETACH IS NOT DESTROY. CloseSession tears down the
// shell/PTY of a session and leaves the sandbox (and anything running inside it)
// alive so a later AttachSandbox can resume it. Only DestroySandbox removes the
// sandbox itself.

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

// ErrUnsupportedOperation is returned when a provider does not implement an
// optional lifecycle capability (attach, detach, destroy).
var ErrUnsupportedOperation = errors.New("sandboxsession: operation not supported by provider")

// SandboxAttacher attaches a new interactive shell session to an already
// provisioned sandbox identified by Request.SandboxUUID.
type SandboxAttacher interface {
	AttachSandbox(ctx context.Context, req Request) (Session, error)
}

// SessionDetacher detaches the interactive shell/PTY of a session WITHOUT
// destroying the sandbox it lives in.
type SessionDetacher interface {
	CloseSession(ctx context.Context, session Session) error
}

// SandboxDestroyer explicitly destroys a sandbox and everything inside it. It is
// the only operation that is allowed to terminate a sandbox.
type SandboxDestroyer interface {
	DestroySandbox(ctx context.Context, ref SandboxRef) error
}

// LifecycleProvider is the optional full lifecycle surface a continuable-session
// provider implements on top of Provider.
type LifecycleProvider interface {
	Provider
	SandboxAttacher
	SessionDetacher
	SandboxDestroyer
}

// SupportsAttach reports whether the resolved provider can attach to an existing
// sandbox.
func SupportsAttach(p Provider) bool {
	_, ok := p.(SandboxAttacher)
	return ok
}

// SupportsDetach reports whether the resolved provider can detach a shell
// without destroying the sandbox.
func SupportsDetach(p Provider) bool {
	_, ok := p.(SessionDetacher)
	return ok
}

// SupportsDestroy reports whether the resolved provider can explicitly destroy a
// sandbox.
func SupportsDestroy(p Provider) bool {
	_, ok := p.(SandboxDestroyer)
	return ok
}

// resolveProvider resolves the provider for an explicit provider name, falling
// back to the single registered provider.
func resolveProvider(reg *Registry, name string) (Provider, error) {
	if reg == nil {
		reg = defaultRegistry
	}
	return reg.resolve(name)
}

// AttachSandbox attaches a new shell session to an existing sandbox: it never
// provisions a sandbox and never destroys one. Request.SandboxUUID is required
// and Request.Template must be empty (see Normalize).
func AttachSandbox(ctx context.Context, reg *Registry, req Request) (Session, error) {
	req.SandboxUUID = strings.TrimSpace(req.SandboxUUID)
	if req.SandboxUUID == "" {
		return Session{}, fmt.Errorf("%w: sandbox_uuid is required to attach", ErrInvalidRequest)
	}
	if strings.TrimSpace(req.Template) != "" {
		return Session{}, fmt.Errorf("%w: template and sandbox_uuid are mutually exclusive", ErrInvalidRequest)
	}
	if err := Normalize(&req); err != nil {
		return Session{}, err
	}

	provider, err := resolveProvider(reg, req.Provider)
	if err != nil {
		return Session{}, err
	}
	name := provider.Name()

	attacher, ok := provider.(SandboxAttacher)
	if !ok {
		return Session{}, fmt.Errorf("sandboxsession: %s: %w: attach", name, ErrUnsupportedOperation)
	}

	session, err := attacher.AttachSandbox(ctx, req)
	if err != nil {
		return Session{}, fmt.Errorf("sandboxsession: %s: attach sandbox %q: %w", name, req.SandboxUUID, err)
	}
	if session.Provider == "" {
		session.Provider = name
	}
	if session.SandboxUUID == "" {
		session.SandboxUUID = req.SandboxUUID
	}
	return session, nil
}

// CloseSession detaches a shell session: the PTY/interactive process is torn
// down, the sandbox stays alive. Closing an already closed session is not an
// error at this layer; providers are expected to be idempotent so a TUI detach
// and a websocket close can both fire for the same session.
func CloseSession(ctx context.Context, reg *Registry, session Session) error {
	providerName := strings.TrimSpace(session.Provider)
	if providerName == "" && strings.TrimSpace(session.SandboxUUID) == "" {
		return fmt.Errorf("%w: session.provider or session.sandbox_uuid is required", ErrInvalidRequest)
	}
	if strings.TrimSpace(session.SessionID) == "" {
		return fmt.Errorf("%w: session.session_id is required", ErrInvalidRequest)
	}

	provider, err := resolveProvider(reg, providerName)
	if err != nil {
		return err
	}
	detacher, ok := provider.(SessionDetacher)
	if !ok {
		return fmt.Errorf("sandboxsession: %s: %w: detach", provider.Name(), ErrUnsupportedOperation)
	}
	if err := detacher.CloseSession(ctx, session); err != nil {
		return fmt.Errorf("sandboxsession: %s: detach session %q: %w", provider.Name(), session.SessionID, err)
	}
	return nil
}

// DestroySandbox explicitly destroys a sandbox. This is the ONLY path that is
// allowed to terminate a sandbox; detaching a session must never call it.
func DestroySandbox(ctx context.Context, reg *Registry, ref SandboxRef) error {
	if strings.TrimSpace(ref.SandboxUUID) == "" {
		return fmt.Errorf("%w: sandbox ref has no sandbox_uuid", ErrInvalidRequest)
	}

	provider, err := resolveProvider(reg, ref.Provider)
	if err != nil {
		return err
	}
	destroyer, ok := provider.(SandboxDestroyer)
	if !ok {
		return fmt.Errorf("sandboxsession: %s: %w: destroy", provider.Name(), ErrUnsupportedOperation)
	}
	if err := destroyer.DestroySandbox(ctx, ref); err != nil {
		return fmt.Errorf("sandboxsession: %s: destroy sandbox %q: %w", provider.Name(), ref.SandboxUUID, err)
	}
	return nil
}
