package nativecoder

import (
	"strings"
	"testing"
)

func TestSandboxToolMissingHintCoversToolchain(t *testing.T) {
	hint, ok := SandboxToolMissingHint("go")
	if !ok || !strings.Contains(hint, SandboxGoBinDir) {
		t.Fatalf("expected a go hint naming %s, got %q ok=%v", SandboxGoBinDir, hint, ok)
	}
	if _, ok := SandboxToolMissingHint("kubectl"); ok {
		t.Fatal("kubectl is provider specific and must not be claimed by the toolchain policy")
	}
	if _, ok := SandboxToolMissingHint("  "); ok {
		t.Fatal("a blank tool name must not be claimed")
	}
}

func TestSandboxToolchainPathEntriesAreAbsoluteAndOrdered(t *testing.T) {
	if len(SandboxToolchainPathEntries) == 0 {
		t.Fatal("expected the toolchain PATH entries to be non-empty")
	}
	for _, entry := range SandboxToolchainPathEntries {
		if !strings.HasPrefix(entry, "/") {
			t.Fatalf("PATH entry %q must be absolute", entry)
		}
	}
	if SandboxToolchainPathEntries[0] != SandboxGoBinDir {
		t.Fatalf("the Go bin dir must win over any image-provided go shim, got %q", SandboxToolchainPathEntries[0])
	}
}

func TestSandboxPrivateModuleEnvHasNoSecrets(t *testing.T) {
	env := SandboxPrivateModuleEnv()
	if env["GOPRIVATE"] != SandboxPrivateModuleOrg || env["GONOSUMDB"] != SandboxPrivateModuleOrg {
		t.Fatalf("unexpected module opt-out: %v", env)
	}
	for key, value := range env {
		lowered := strings.ToLower(key + "=" + value)
		for _, secret := range []string{"token", "secret", "password", "credential"} {
			if strings.Contains(lowered, secret) {
				t.Fatalf("env %s must never carry credentials: %v", key, value)
			}
		}
	}
}

func TestSandboxPrivateModuleHintDetectsUnmaterializedWorkspace(t *testing.T) {
	hint, ok := SandboxPrivateModuleHint("go: github.com/msgmate-io/open-chat-go-ci/integrations: module lookup disabled by GOPRIVATE=github.com/msgmate-io/*")
	if !ok {
		t.Fatal("expected the module lookup failure to be recognised")
	}
	if !strings.Contains(hint, SandboxProfileSetupCommand) {
		t.Fatalf("hint must name the profile setup command, got %q", hint)
	}
	if strings.Contains(hint, "secret") || strings.Contains(hint, "token") {
		t.Fatalf("hint must stay credential-free, got %q", hint)
	}
}

func TestSandboxPrivateModuleHintIgnoresOrdinaryCompileErrors(t *testing.T) {
	if _, ok := SandboxPrivateModuleHint("backend/api/msgmate/openai.go:42: undefined: foo"); ok {
		t.Fatal("a plain compile error must not be misread as a workspace problem")
	}
	if _, ok := SandboxPrivateModuleHint(""); ok {
		t.Fatal("empty output must not be recognised")
	}
}

func TestSandboxToolMissingHintFromOutputDetectsShellMissingTool(t *testing.T) {
	hint, ok := SandboxToolMissingHintFromOutput("/bin/sh: 1: go: not found")
	if !ok || !strings.Contains(hint, SandboxGoBinDir) {
		t.Fatalf("expected a go PATH hint, got %q ok=%v", hint, ok)
	}
	if hint, ok := SandboxToolMissingHintFromOutput("sh: kubectl: not found"); ok {
		t.Fatalf("provider-specific binaries are not owned by this policy, got %q", hint)
	}
	if hint, ok := SandboxToolMissingHintFromOutput("app/main.go:12:3: syntax error"); ok {
		t.Fatalf("ordinary build errors must not be reinterpreted, got %q", hint)
	}
}

func TestSandboxFailureGuidanceOnlyClaimsCoderTools(t *testing.T) {
	output := "go: module lookup disabled by GOPRIVATE"
	if got := SandboxFailureGuidance("coder_bash", output); got == "" {
		t.Fatal("expected guidance for a coder tool failure")
	}
	if got := SandboxFailureGuidance("send_message", output); got != "" {
		t.Fatalf("non-coder tools must be left alone, got %q", got)
	}
	if got := SandboxFailureGuidance("coder_bash", "go test: FAIL backend/api/msgmate [build failed]"); got != "" {
		t.Fatalf("a real test failure must keep its own message, got %q", got)
	}
}

func TestFrameSandboxFailureGuidanceAppendsHintOnce(t *testing.T) {
	original := "sh: 1: gofmt: not found"
	framed := FrameSandboxFailureGuidance("coder_bash", original)
	if !strings.HasPrefix(framed, original) {
		t.Fatalf("the original payload must stay verbatim, got %q", framed)
	}
	if !strings.Contains(framed, SandboxGoBinDir) {
		t.Fatalf("expected the toolchain hint in %q", framed)
	}
	if again := FrameSandboxFailureGuidance("coder_bash", original); again != framed {
		t.Fatalf("framing must be deterministic: %q != %q", again, framed)
	}
	untouched := "regular tool output"
	if got := FrameSandboxFailureGuidance("coder_read", untouched); got != untouched {
		t.Fatalf("a clean result must not be rewritten, got %q", got)
	}
}
