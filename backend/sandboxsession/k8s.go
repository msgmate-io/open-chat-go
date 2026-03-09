package sandboxsession

import (
	"errors"
	"fmt"
	"strings"

	"go.yaml.in/yaml/v3"
)

// This file implements the kubernetes flavour of a sandbox shell session
// (ci#690): render the kubeconfig of one registered cluster into the sandbox at
// a well-known path and attach a k8s TUI (k9s, falling back to kubectl) shell
// session to it.

const (
	// KubeconfigPath is where a rendered kubeconfig.yaml is injected.
	KubeconfigPath = "/workspace/.kube/config"
	// KubeTUIDefault is the interactive TUI attached by default.
	KubeTUIDefault = "k9s"
	// KubeTUIFallback is used when k9s is not installed in the sandbox image.
	KubeTUIFallback = "kubectl"
	// KubeProviderName is the conventional registry key of a k8s sandbox
	// provider (see the k8s sandbox integration).
	KubeProviderName = "k8s_sandbox"
)

// KubeTUIOptions configures the TUI command attached to a k8s sandbox.
type KubeTUIOptions struct {
	// TUI is "k9s" (default) or "kubectl"; anything else is rejected.
	TUI string
	// Context selects a kubeconfig context (optional).
	Context string
	// Namespace optionally pins the initial namespace.
	Namespace string
	// ExtraArgs are appended verbatim.
	ExtraArgs []string
	// WorkDir overrides ShellSpec.WorkDir.
	WorkDir string
	// Env is merged into the session environment.
	Env map[string]string
}

// KubeTUICommand builds the interactive argv for the given TUI. The kubeconfig
// path is always passed explicitly so the session does not depend on $HOME.
func KubeTUICommand(opts KubeTUIOptions) ([]string, error) {
	tui := strings.ToLower(strings.TrimSpace(opts.TUI))
	if tui == "" {
		tui = KubeTUIDefault
	}

	var argv []string
	switch tui {
	case "k9s", "kubectl":
		argv = []string{tui}
	default:
		return nil, fmt.Errorf("%w: unsupported k8s tui %q (want %s or %s)", ErrInvalidRequest, opts.TUI, KubeTUIDefault, KubeTUIFallback)
	}

	if tui == "k9s" {
		argv = append(argv, "--kubeconfig", KubeconfigPath)
		if ctx := strings.TrimSpace(opts.Context); ctx != "" {
			argv = append(argv, "--context", ctx)
		}
		if ns := strings.TrimSpace(opts.Namespace); ns != "" {
			argv = append(argv, "--namespace", ns)
		}
	} else {
		// kubectl has no TUI; `kubectl proxy` + web UI is out of scope here, so a
		// kubectl session is an interactive get/watch loop via `kubectl shell`
		// replacement: run kubectl itself with a helpful default.
		argv = append(argv, "--kubeconfig", KubeconfigPath)
		if ctx := strings.TrimSpace(opts.Context); ctx != "" {
			argv = append(argv, "--context", ctx)
		}
		if ns := strings.TrimSpace(opts.Namespace); ns != "" {
			argv = append(argv, "--namespace", ns)
		}
		argv = append(argv, "get", "pods", "-w")
	}

	for _, extra := range opts.ExtraArgs {
		if strings.TrimSpace(extra) == "" {
			return nil, fmt.Errorf("%w: empty k8s tui extra arg", ErrInvalidRequest)
		}
		argv = append(argv, extra)
	}
	return argv, nil
}

// ValidateKubeconfig performs a cheap structural check so an obviously broken
// kubeconfig never reaches the sandbox. It deliberately does not resolve
// credentials or contact the cluster.
func ValidateKubeconfig(kubeconfig []byte) error {
	if strings.TrimSpace(string(kubeconfig)) == "" {
		return fmt.Errorf("%w: kubeconfig is empty", ErrInvalidRequest)
	}

	var doc struct {
		APIVersion     string      `yaml:"apiVersion"`
		Kind           string      `yaml:"kind"`
		CurrentContext string      `yaml:"current-context"`
		Clusters       []yaml.Node `yaml:"clusters"`
		Contexts       []yaml.Node `yaml:"contexts"`
		Users          []yaml.Node `yaml:"users"`
	}
	if err := yaml.Unmarshal(kubeconfig, &doc); err != nil {
		return fmt.Errorf("%w: kubeconfig is not valid YAML: %v", ErrInvalidRequest, err)
	}
	if strings.TrimSpace(doc.APIVersion) == "" {
		return fmt.Errorf("%w: kubeconfig has no apiVersion", ErrInvalidRequest)
	}
	if len(doc.Clusters) == 0 {
		return fmt.Errorf("%w: kubeconfig has no clusters", ErrInvalidRequest)
	}
	if len(doc.Contexts) == 0 {
		return fmt.Errorf("%w: kubeconfig has no contexts", ErrInvalidRequest)
	}
	return nil
}

// KubeRequestOptions describes the "k8s button" flow of a kubernetes cluster
// overview page: one registered cluster, its kubeconfig and a TUI.
type KubeRequestOptions struct {
	// Provider defaults to KubeProviderName.
	Provider string
	// OwnerUUID is the admin the session belongs to.
	OwnerUUID string
	// OwnerUsername is a human-readable label (optional).
	OwnerUsername string
	// ClusterName is the registered cluster name; it names the sandbox and is
	// the kubeconfig context when Context is empty.
	ClusterName string
	// Kubeconfig is the rendered kubeconfig.yaml content for the cluster.
	Kubeconfig []byte
	// KubeconfigPath overrides KubeconfigPath.
	KubeconfigPath string
	// Template/Image optionally pin the sandbox image.
	Template string
	Image    string
	// SandboxName overrides the derived sandbox name.
	SandboxName string
	// TUI configures the attached k8s TUI shell.
	TUI KubeTUIOptions
}

// BuildKubeRequest assembles the sandbox + kubeconfig injection + k8s TUI shell
// request for one cluster. The kubeconfig content is only carried in the
// FileInjection; nothing else echoes it.
func BuildKubeRequest(opts KubeRequestOptions) (Request, error) {
	cluster := strings.TrimSpace(opts.ClusterName)
	if cluster == "" {
		return Request{}, fmt.Errorf("%w: cluster name is required", ErrInvalidRequest)
	}
	if err := ValidateKubeconfig(opts.Kubeconfig); err != nil {
		return Request{}, err
	}

	path := strings.TrimSpace(opts.KubeconfigPath)
	if path == "" {
		path = KubeconfigPath
	}

	tui := opts.TUI
	if strings.TrimSpace(tui.Context) == "" {
		tui.Context = cluster
	}
	argv, err := KubeTUICommand(tui)
	if err != nil {
		return Request{}, err
	}

	provider := strings.TrimSpace(opts.Provider)
	if provider == "" {
		provider = KubeProviderName
	}

	sandboxName := strings.TrimSpace(opts.SandboxName)
	if sandboxName == "" {
		sandboxName = "k8s-" + sanitizeName(cluster)
	}

	req := Request{
		Provider:      provider,
		OwnerUUID:     opts.OwnerUUID,
		OwnerUsername: opts.OwnerUsername,
		SandboxName:   sandboxName,
		Template:      opts.Template,
		Image:         opts.Image,
		Files: []FileInjection{{
			Path:    path,
			Content: opts.Kubeconfig,
			Mode:    0o600,
			Secret:  true,
		}},
		Shell: ShellSpec{
			Command: argv,
			WorkDir: tui.WorkDir,
			Env:     tui.Env,
		},
	}
	if err := Normalize(&req); err != nil {
		return Request{}, err
	}
	return req, nil
}

// sanitizeName turns a cluster name into a sandbox-safe slug.
func sanitizeName(name string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(strings.TrimSpace(name)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-', r == '_', r == '.':
			b.WriteRune(r)
		default:
			b.WriteRune('-')
		}
	}
	slug := strings.Trim(b.String(), "-_.")
	if slug == "" {
		return "cluster"
	}
	if len(slug) > 40 {
		slug = strings.TrimRight(slug[:40], "-_.")
	}
	return slug
}

// ErrNoSandboxProvider is kept as an alias so integration code that predates
// the rename still builds.
var ErrNoSandboxProvider = ErrNoProvider

// IsNoProvider reports whether err means "no sandbox integration is compiled
// in", which the UI should render as a disabled k8s button.
func IsNoProvider(err error) bool {
	return errors.Is(err, ErrNoProvider)
}
