package sandboxsession

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"
)

// fakeProvider records the flow Open drives it through.
type fakeProvider struct {
	name string

	ensureCalls  int
	injectCalls  int
	sessionCalls int

	gotFiles  []FileInjection
	gotSpec   ShellSpec
	gotReq    Request
	ensureErr error
	injectErr error
	sessErr   error

	returnedRef SandboxRef
}

func (f *fakeProvider) Name() string { return f.name }

func (f *fakeProvider) EnsureSandbox(_ context.Context, req Request) (SandboxRef, error) {
	f.ensureCalls++
	f.gotReq = req
	if f.ensureErr != nil {
		return SandboxRef{}, f.ensureErr
	}
	ref := f.returnedRef
	ref.SandboxUUID = "sbx-1"
	ref.Name = req.SandboxName
	return ref, nil
}

func (f *fakeProvider) InjectFiles(_ context.Context, _ SandboxRef, files []FileInjection) error {
	f.injectCalls++
	f.gotFiles = files
	return f.injectErr
}

func (f *fakeProvider) OpenSession(_ context.Context, _ SandboxRef, spec ShellSpec) (Session, error) {
	f.sessionCalls++
	f.gotSpec = spec
	if f.sessErr != nil {
		return Session{}, f.sessErr
	}
	return Session{SessionID: "sess-1", AttachPath: "/terminal/sess-1", ExpiresAt: time.Now().Add(time.Hour)}, nil
}

func TestOpenDrivesProviderThroughEnsureInjectAndShell(t *testing.T) {
	reg := NewRegistry()
	p := &fakeProvider{name: "fake"}
	if err := reg.Register(p); err != nil {
		t.Fatalf("register: %v", err)
	}

	session, err := Open(context.Background(), reg, Request{
		OwnerUUID: "user-1",
		Files:     []FileInjection{{Path: "/workspace/.kube/config", Content: []byte("x"), Secret: true}},
	})
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	if p.ensureCalls != 1 || p.injectCalls != 1 || p.sessionCalls != 1 {
		t.Fatalf("expected one ensure/inject/session call, got %d/%d/%d", p.ensureCalls, p.injectCalls, p.sessionCalls)
	}
	if got := Paths(p.gotFiles); len(got) != 1 || got[0] != "/workspace/.kube/config" {
		t.Fatalf("unexpected injected paths: %v", got)
	}
	if p.gotFiles[0].ModeOrDefault() != DefaultFileMode {
		t.Fatalf("expected default file mode %o, got %o", DefaultFileMode, p.gotFiles[0].ModeOrDefault())
	}
	if p.gotSpec.WorkDir != DefaultWorkDir {
		t.Fatalf("expected default workdir %q, got %q", DefaultWorkDir, p.gotSpec.WorkDir)
	}
	if p.gotSpec.TTL != DefaultSessionTTL {
		t.Fatalf("expected default ttl %s, got %s", DefaultSessionTTL, p.gotSpec.TTL)
	}
	if strings.Join(p.gotSpec.Command, " ") != strings.Join(DefaultShellCommand, " ") {
		t.Fatalf("expected default shell command, got %v", p.gotSpec.Command)
	}
	if session.Provider != "fake" {
		t.Fatalf("expected provider to be backfilled, got %q", session.Provider)
	}
	if session.SandboxUUID != "sbx-1" {
		t.Fatalf("expected sandbox uuid from ref, got %q", session.SandboxUUID)
	}
}

func TestOpenRejectsNonAbsoluteOrUncleanInjectionPaths(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(&fakeProvider{name: "fake"}); err != nil {
		t.Fatalf("register: %v", err)
	}

	for _, path := range []string{"relative/config", "/workspace/../etc/shadow", "/workspace/./config"} {
		_, err := Open(context.Background(), reg, Request{Files: []FileInjection{{Path: path}}})
		if !errors.Is(err, ErrInvalidRequest) {
			t.Fatalf("path %q: expected ErrInvalidRequest, got %v", path, err)
		}
	}
}

func TestOpenProviderResolution(t *testing.T) {
	t.Run("no provider", func(t *testing.T) {
		_, err := Open(context.Background(), NewRegistry(), Request{})
		if !IsNoProvider(err) {
			t.Fatalf("expected ErrNoProvider, got %v", err)
		}
	})

	t.Run("ambiguous without selection", func(t *testing.T) {
		reg := NewRegistry()
		_ = reg.Register(&fakeProvider{name: "docker_sandbox"})
		_ = reg.Register(&fakeProvider{name: "k8s_sandbox"})
		_, err := Open(context.Background(), reg, Request{})
		if !errors.Is(err, ErrAmbiguousProvider) {
			t.Fatalf("expected ErrAmbiguousProvider, got %v", err)
		}
		_, err = Open(context.Background(), reg, Request{Provider: "k8s_sandbox"})
		if err != nil {
			t.Fatalf("explicit provider: %v", err)
		}
	})

	t.Run("unknown provider", func(t *testing.T) {
		reg := NewRegistry()
		_ = reg.Register(&fakeProvider{name: "k8s_sandbox"})
		_, err := Open(context.Background(), reg, Request{Provider: "nope"})
		if !errors.Is(err, ErrUnknownProvider) {
			t.Fatalf("expected ErrUnknownProvider, got %v", err)
		}
	})
}

func TestRegistryRejectsDuplicatesAndEmptyNames(t *testing.T) {
	reg := NewRegistry()
	if err := reg.Register(&fakeProvider{name: "k8s_sandbox"}); err != nil {
		t.Fatalf("register: %v", err)
	}
	if err := reg.Register(&fakeProvider{name: "k8s_sandbox"}); err == nil {
		t.Fatal("expected duplicate registration to fail")
	}
	if err := reg.Register(&fakeProvider{name: "  "}); err == nil {
		t.Fatal("expected empty provider name to fail")
	}
	if got := reg.Names(); len(got) != 1 || got[0] != "k8s_sandbox" {
		t.Fatalf("unexpected names: %v", got)
	}
}

func TestOpenSurfacesInjectionFailureWithoutLeakingContent(t *testing.T) {
	reg := NewRegistry()
	p := &fakeProvider{name: "k8s_sandbox", injectErr: errors.New("boom")}
	_ = reg.Register(p)

	secret := []byte("apiVersion: v1\nclusters: []\n")
	_, err := Open(context.Background(), reg, Request{Files: []FileInjection{{Path: KubeconfigPath, Content: secret, Secret: true}}})
	if err == nil {
		t.Fatal("expected injection failure")
	}
	if p.sessionCalls != 0 {
		t.Fatal("shell must not open when injection failed")
	}
	if strings.Contains(err.Error(), string(secret)) {
		t.Fatalf("error leaked file content: %v", err)
	}
	if !strings.Contains(err.Error(), KubeconfigPath) {
		t.Fatalf("expected the injected path in the error, got %v", err)
	}
}

const testKubeconfig = `apiVersion: v1
kind: Config
current-context: prod
clusters:
  - name: prod
    cluster:
      server: https://prod.example:6443
contexts:
  - name: prod
    context:
      cluster: prod
      user: admin
users:
  - name: admin
    user:
      token: super-secret-token
`

func TestBuildKubeRequestInjectsKubeconfigAndK9sTUI(t *testing.T) {
	req, err := BuildKubeRequest(KubeRequestOptions{
		OwnerUUID:   "admin-1",
		ClusterName: "Prod Cluster",
		Kubeconfig:  []byte(testKubeconfig),
	})
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	if req.Provider != KubeProviderName {
		t.Fatalf("expected provider %q, got %q", KubeProviderName, req.Provider)
	}
	if req.OwnerUUID != "admin-1" {
		t.Fatalf("owner not propagated: %q", req.OwnerUUID)
	}
	if req.SandboxName != "k8s-prod-cluster" {
		t.Fatalf("expected slugged sandbox name, got %q", req.SandboxName)
	}
	if len(req.Files) != 1 || req.Files[0].Path != KubeconfigPath {
		t.Fatalf("expected one kubeconfig injection at %q, got %v", KubeconfigPath, Paths(req.Files))
	}
	if !req.Files[0].Secret || req.Files[0].ModeOrDefault() != 0o600 {
		t.Fatalf("kubeconfig must be injected as a 0600 secret, got secret=%v mode=%o", req.Files[0].Secret, req.Files[0].ModeOrDefault())
	}
	if string(req.Files[0].Content) != testKubeconfig {
		t.Fatal("kubeconfig content was mutated")
	}

	want := []string{"k9s", "--kubeconfig", KubeconfigPath, "--context", "Prod Cluster"}
	if strings.Join(req.Shell.Command, " ") != strings.Join(want, " ") {
		t.Fatalf("expected %v, got %v", want, req.Shell.Command)
	}
	if req.Shell.DisableTTY {
		t.Fatal("a TUI shell needs a tty")
	}
}

func TestBuildKubeRequestRejectsBadInput(t *testing.T) {
	if _, err := BuildKubeRequest(KubeRequestOptions{Kubeconfig: []byte(testKubeconfig)}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected missing cluster name to fail, got %v", err)
	}
	if _, err := BuildKubeRequest(KubeRequestOptions{ClusterName: "c", Kubeconfig: []byte("   ")}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected empty kubeconfig to fail, got %v", err)
	}
	if _, err := BuildKubeRequest(KubeRequestOptions{ClusterName: "c", Kubeconfig: []byte("apiVersion: v1\ncontexts: []\n")}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected kubeconfig without clusters to fail, got %v", err)
	}
	if _, err := BuildKubeRequest(KubeRequestOptions{ClusterName: "c", Kubeconfig: []byte(testKubeconfig), TUI: KubeTUIOptions{TUI: "vim"}}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected unknown tui to fail, got %v", err)
	}
}

func TestKubeTUICommandVariants(t *testing.T) {
	argv, err := KubeTUICommand(KubeTUIOptions{TUI: "k9s", Namespace: "kube-system"})
	if err != nil {
		t.Fatalf("k9s: %v", err)
	}
	want := []string{"k9s", "--kubeconfig", KubeconfigPath, "--namespace", "kube-system"}
	if strings.Join(argv, " ") != strings.Join(want, " ") {
		t.Fatalf("expected %v, got %v", want, argv)
	}

	argv, err = KubeTUICommand(KubeTUIOptions{TUI: "kubectl", Context: "prod"})
	if err != nil {
		t.Fatalf("kubectl: %v", err)
	}
	want = []string{"kubectl", "--kubeconfig", KubeconfigPath, "--context", "prod", "get", "pods", "-w"}
	if strings.Join(argv, " ") != strings.Join(want, " ") {
		t.Fatalf("expected %v, got %v", want, argv)
	}

	if _, err := KubeTUICommand(KubeTUIOptions{TUI: "k9s", ExtraArgs: []string{" "}}); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected empty extra arg to fail, got %v", err)
	}
}

func TestValidateKubeconfigAcceptsRealShapes(t *testing.T) {
	if err := ValidateKubeconfig([]byte(testKubeconfig)); err != nil {
		t.Fatalf("expected valid kubeconfig, got %v", err)
	}
	if err := ValidateKubeconfig([]byte("not: [valid")); !errors.Is(err, ErrInvalidRequest) {
		t.Fatalf("expected invalid yaml to fail, got %v", err)
	}
}

func TestOpenEndToEndWithKubernetesFlavour(t *testing.T) {
	reg := NewRegistry()
	p := &fakeProvider{name: KubeProviderName}
	if err := reg.Register(p); err != nil {
		t.Fatalf("register: %v", err)
	}

	req, err := BuildKubeRequest(KubeRequestOptions{OwnerUUID: "admin-1", ClusterName: "prod", Kubeconfig: []byte(testKubeconfig)})
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	session, err := Open(context.Background(), reg, req)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if session.Provider != KubeProviderName || session.SessionID == "" {
		t.Fatalf("unexpected session: %+v", session)
	}
	if p.gotReq.SandboxName != "k8s-prod" {
		t.Fatalf("expected sandbox name to reach the provider, got %q", p.gotReq.SandboxName)
	}
	if len(p.gotFiles) != 1 || !p.gotFiles[0].Secret {
		t.Fatalf("expected the kubeconfig to be injected as a secret, got %v", Paths(p.gotFiles))
	}
	if p.gotSpec.Command[0] != "k9s" || p.gotSpec.TTL <= 0 {
		t.Fatalf("unexpected shell spec: %+v", p.gotSpec)
	}
}

func TestPathsNeverIncludeContent(t *testing.T) {
	got := Paths([]FileInjection{
		{Path: "/b", Content: []byte("secret-b")},
		{Path: "/a", Content: []byte("secret-a")},
	})
	if len(got) != 2 || got[0] != "/a" || got[1] != "/b" {
		t.Fatalf("expected sorted paths, got %v", got)
	}
	for _, p := range got {
		if strings.Contains(p, "secret") {
			t.Fatalf("path list contains file content: %v", got)
		}
	}
}

func TestDefaultFileModeIsOwnerOnly(t *testing.T) {
	if DefaultFileMode != os.FileMode(0o600) {
		t.Fatalf("credential files must default to 0600, got %o", DefaultFileMode)
	}
}
