// Package sandboxsession defines the core contract for "sandbox shell
// sessions": an ephemeral sandbox plus files injected into it plus an attached
// interactive TUI shell (ci#690, "k8s sandbox").
//
// The point of the contract is that a sandbox-providing integration (docker
// sandbox integration today, a k8s sandbox provider tomorrow) and a consuming
// integration (the kubernetes integration, which injects a rendered
// kubeconfig.yaml and attaches a k9s TUI shell to a registered cluster) do not
// have to import each other: providers register themselves in the
// process-wide registry at init time and consumers drive them through Open.
//
// Nothing in this package logs or returns file *contents*: only target paths,
// so credential material such as a kubeconfig never leaks into logs or the UI.
package sandboxsession

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path"
	"sort"
	"strings"
	"sync"
	"time"
)

// DefaultFileMode is used when FileInjection.Mode is zero.
const DefaultFileMode os.FileMode = 0o600

// DefaultWorkDir is the working directory a shell session starts in.
const DefaultWorkDir = "/workspace"

// DefaultSessionTTL bounds how long an unattended admin shell may live.
const DefaultSessionTTL = time.Hour

// DefaultShellCommand is the fallback interactive command.
var DefaultShellCommand = []string{"/bin/sh", "-l"}

// Sentinel errors callers can match with errors.Is.
var (
	ErrNoProvider        = errors.New("sandboxsession: no sandbox provider registered")
	ErrAmbiguousProvider = errors.New("sandboxsession: multiple sandbox providers registered, request.Provider is required")
	ErrUnknownProvider   = errors.New("sandboxsession: unknown sandbox provider")
	ErrInvalidRequest    = errors.New("sandboxsession: invalid request")
)

// FileInjection is a single file written into a sandbox before its shell
// session is opened.
type FileInjection struct {
	// Path is the absolute target path inside the sandbox.
	Path string
	// Content is the raw file content (eg a rendered kubeconfig.yaml).
	Content []byte
	// Mode is the target file mode; zero means DefaultFileMode.
	Mode os.FileMode
	// Secret marks credential material. Providers must redact such files from
	// logs and apply at least default secret permissions.
	Secret bool
}

// ModeOrDefault returns the effective file mode.
func (f FileInjection) ModeOrDefault() os.FileMode {
	if f.Mode != 0 {
		return f.Mode
	}
	return DefaultFileMode
}

// Paths returns the injected target paths, sorted, for logging and UI
// previews. File contents are never included.
func Paths(files []FileInjection) []string {
	out := make([]string, 0, len(files))
	for _, file := range files {
		out = append(out, file.Path)
	}
	sort.Strings(out)
	return out
}

// ShellSpec describes the interactive command attached to a sandbox.
type ShellSpec struct {
	// Command is the argv of the interactive process (eg ["k9s", "--kubeconfig", ...]).
	Command []string
	// WorkDir defaults to DefaultWorkDir.
	WorkDir string
	// Env is added to the session environment.
	Env map[string]string
	// DisableTTY opts out of the pty allocation that TUI shells require.
	DisableTTY bool
	// TTL defaults to DefaultSessionTTL.
	TTL time.Duration
}

// Request is a complete "sandbox + injected files + attached TUI shell" request.
type Request struct {
	// Provider selects a registered provider. It may be empty when exactly one
	// provider is registered.
	Provider string
	// OwnerUUID is the user the sandbox and session belong to.
	OwnerUUID string
	// OwnerUsername is a human-readable owner label (optional).
	OwnerUsername string
	// SandboxName requests a stable, named sandbox (optional; providers may
	// reuse an existing sandbox by name).
	SandboxName string
	// Template selects a sandbox template (optional, provider specific).
	Template string
	// Image overrides the sandbox image (optional, provider specific).
	Image string
	// Files are injected before the shell session is opened.
	Files []FileInjection
	// Shell describes the interactive session.
	Shell ShellSpec
}

// SandboxRef identifies a provisioned sandbox.
type SandboxRef struct {
	Provider    string
	SandboxUUID string
	Name        string
}

// Session describes an attached interactive shell session.
type Session struct {
	Provider    string
	SandboxUUID string
	SessionID   string
	// AttachPath is the (frontend relative) URL the UI opens to attach.
	AttachPath string
	// AttachToken is a short-lived token guarding the attach endpoint.
	AttachToken string
	ExpiresAt   time.Time
}

// Provider is implemented by a sandbox-providing integration.
type Provider interface {
	// Name is the registry key, eg "docker_sandbox" or "k8s_sandbox".
	Name() string
	// EnsureSandbox provisions or reuses a sandbox for the request.
	EnsureSandbox(ctx context.Context, req Request) (SandboxRef, error)
	// InjectFiles writes the request files into the sandbox.
	InjectFiles(ctx context.Context, ref SandboxRef, files []FileInjection) error
	// OpenSession starts the interactive shell session.
	OpenSession(ctx context.Context, ref SandboxRef, spec ShellSpec) (Session, error)
}

// Registry holds the registered sandbox providers.
type Registry struct {
	mu        sync.RWMutex
	providers map[string]Provider
	names     []string
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry {
	return &Registry{providers: make(map[string]Provider)}
}

var defaultRegistry = NewRegistry()

// DefaultRegistry returns the process-wide registry providers register with at
// init time.
func DefaultRegistry() *Registry { return defaultRegistry }

// Register adds a provider. Duplicate, nil and unnamed providers are rejected.
func (r *Registry) Register(p Provider) error {
	if p == nil {
		return fmt.Errorf("%w: nil sandbox provider", ErrInvalidRequest)
	}
	name := strings.TrimSpace(p.Name())
	if name == "" {
		return fmt.Errorf("%w: sandbox provider name is empty", ErrInvalidRequest)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if r.providers == nil {
		r.providers = make(map[string]Provider)
	}
	if _, exists := r.providers[name]; exists {
		return fmt.Errorf("%w: sandbox provider %q already registered", ErrInvalidRequest, name)
	}
	r.providers[name] = p
	r.names = append(r.names, name)
	sort.Strings(r.names)
	return nil
}

// Lookup returns a provider by name.
func (r *Registry) Lookup(name string) (Provider, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	p, ok := r.providers[strings.TrimSpace(name)]
	return p, ok
}

// Names returns the registered provider names, sorted.
func (r *Registry) Names() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return append([]string(nil), r.names...)
}

// resolve picks the provider for a request.
func (r *Registry) resolve(name string) (Provider, error) {
	name = strings.TrimSpace(name)
	if name != "" {
		if p, ok := r.Lookup(name); ok {
			return p, nil
		}
		return nil, fmt.Errorf("%w: %q (registered: %s)", ErrUnknownProvider, name, strings.Join(r.Names(), ", "))
	}

	names := r.Names()
	switch len(names) {
	case 0:
		return nil, ErrNoProvider
	case 1:
		p, _ := r.Lookup(names[0])
		return p, nil
	default:
		return nil, fmt.Errorf("%w (registered: %s)", ErrAmbiguousProvider, strings.Join(names, ", "))
	}
}

// Normalize trims and defaults a request in place and validates its shape.
func Normalize(req *Request) error {
	if req == nil {
		return fmt.Errorf("%w: nil request", ErrInvalidRequest)
	}

	req.Provider = strings.TrimSpace(req.Provider)
	req.OwnerUUID = strings.TrimSpace(req.OwnerUUID)
	req.OwnerUsername = strings.TrimSpace(req.OwnerUsername)
	req.SandboxName = strings.TrimSpace(req.SandboxName)
	req.Template = strings.TrimSpace(req.Template)
	req.Image = strings.TrimSpace(req.Image)

	if len(req.Shell.Command) == 0 {
		req.Shell.Command = append([]string(nil), DefaultShellCommand...)
	}
	for i, arg := range req.Shell.Command {
		if strings.TrimSpace(arg) == "" {
			return fmt.Errorf("%w: shell.command[%d] is empty", ErrInvalidRequest, i)
		}
	}
	if strings.TrimSpace(req.Shell.WorkDir) == "" {
		req.Shell.WorkDir = DefaultWorkDir
	}
	if req.Shell.TTL <= 0 {
		req.Shell.TTL = DefaultSessionTTL
	}

	for i := range req.Files {
		file := &req.Files[i]
		file.Path = strings.TrimSpace(file.Path)
		if file.Path == "" {
			return fmt.Errorf("%w: files[%d]: path is empty", ErrInvalidRequest, i)
		}
		if !path.IsAbs(file.Path) {
			return fmt.Errorf("%w: files[%d]: path %q must be absolute", ErrInvalidRequest, i, file.Path)
		}
		if cleaned := path.Clean(file.Path); cleaned != file.Path {
			return fmt.Errorf("%w: files[%d]: path %q must be clean (%q)", ErrInvalidRequest, i, file.Path, cleaned)
		}
	}

	return nil
}

// Open drives a provider through the whole flow: ensure the sandbox, inject the
// files and attach the shell session.
func Open(ctx context.Context, reg *Registry, req Request) (Session, error) {
	if reg == nil {
		reg = defaultRegistry
	}
	if err := Normalize(&req); err != nil {
		return Session{}, err
	}

	provider, err := reg.resolve(req.Provider)
	if err != nil {
		return Session{}, err
	}
	name := provider.Name()

	ref, err := provider.EnsureSandbox(ctx, req)
	if err != nil {
		return Session{}, fmt.Errorf("sandboxsession: %s: ensure sandbox: %w", name, err)
	}
	if ref.Provider == "" {
		ref.Provider = name
	}

	if len(req.Files) > 0 {
		if err := provider.InjectFiles(ctx, ref, req.Files); err != nil {
			return Session{}, fmt.Errorf("sandboxsession: %s: inject files %v: %w", name, Paths(req.Files), err)
		}
	}

	session, err := provider.OpenSession(ctx, ref, req.Shell)
	if err != nil {
		return Session{}, fmt.Errorf("sandboxsession: %s: open shell session: %w", name, err)
	}
	if session.Provider == "" {
		session.Provider = name
	}
	if session.SandboxUUID == "" {
		session.SandboxUUID = ref.SandboxUUID
	}
	return session, nil
}
