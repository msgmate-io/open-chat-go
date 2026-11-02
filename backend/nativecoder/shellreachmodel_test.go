package nativecoder

import (
	"errors"
	"testing"
)

func TestCoderShellShortVerb(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    string
	}{
		{name: "empty", command: "", want: ""},
		{name: "whitespace", command: "   \n\t ", want: ""},
		{name: "plain verb", command: "go test ./...", want: "go"},
		{name: "absolute binary", command: "/usr/local/go/bin/go version", want: "/usr/local/go/bin/go"},
		{name: "cd prefix", command: "cd backend && go build ./nativecoder/", want: "go"},
		{name: "cd hoist loop", command: "cd a && cd b && make all", want: "make"},
		{name: "case folded", command: "GIT status", want: "git"},
		{name: "leader whitespace", command: "   tail -f log/app.log", want: "tail"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CoderShellShortVerb(tt.command); got != tt.want {
				t.Fatalf("CoderShellShortVerb(%q) = %q, want %q", tt.command, got, tt.want)
			}
		})
	}
}

func TestCoderShellKindForCommand(t *testing.T) {
	tests := []struct {
		name    string
		command string
		want    CoderShellCallKind
	}{
		{name: "empty falls back to run", command: "", want: CoderShellCallRun},
		{name: "single shot", command: "go test ./backend/nativecoder/", want: CoderShellCallRun},
		{name: "dev server", command: "cd backend && cargo dev serve", want: CoderShellCallLive},
		{name: "serve", command: "npm run dev", want: CoderShellCallLive},
		{name: "watcher", command: "cd frontend && npm run watch", want: CoderShellCallLive},
		{name: "tail follow", command: "tail -f tmp/server.log", want: CoderShellCallLive},
		{name: "wait watch", command: "wait \"$(pgrep node)\"", want: CoderShellCallLive},
		{name: "repl", command: "repl --quiet", want: CoderShellCallInteract},
		{name: "python repl", command: "python -i", want: CoderShellCallInteract},
		{name: "ipython", command: "ipython", want: CoderShellCallInteract},
		{name: "node repl", command: "node -e \"console.log(1)\"", want: CoderShellCallInteract},
		{name: "unknown verb is run", command: "curl -sI http://localhost:8080/", want: CoderShellCallRun},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CoderShellKindForCommand(tt.command); got != tt.want {
				t.Fatalf("CoderShellKindForCommand(%q) = %q, want %q", tt.command, got, tt.want)
			}
		})
	}
}

func TestCoderShellDetectKind(t *testing.T) {
	if _, err := CoderShellDetectKind(""); !errors.Is(err, ErrCoderShellDetectCommand) {
		t.Fatalf("CoderShellDetectKind(\"\") error = %v, want ErrCoderShellDetectCommand", err)
	}
	kind, err := CoderShellDetectKind("tail -f tmp/server.log")
	if err != nil {
		t.Fatalf("CoderShellDetectKind(live) unexpected error: %v", err)
	}
	if kind != CoderShellCallLive {
		t.Fatalf("CoderShellDetectKind(live) = %q, want %q", kind, CoderShellCallLive)
	}
}

func TestCoderShellShowTellDetector(t *testing.T) {
	d := CoderShellShowTellDetector()
	if got := d.DetectKind("cargo dev serve"); got != CoderShellCallLive {
		t.Fatalf("detector live kind = %q, want %q", got, CoderShellCallLive)
	}
	if got := d.DetectKind("python -i"); got != CoderShellCallInteract {
		t.Fatalf("detector interact kind = %q, want %q", got, CoderShellCallInteract)
	}
	if got := d.DetectKind("go test ./..."); got != CoderShellCallRun {
		t.Fatalf("detector run kind = %q, want %q", got, CoderShellCallRun)
	}
}

func TestCoderShellKindVerbsMatchMap(t *testing.T) {
	// The exported verb lists must agree with the classification map so an
	// adapter and the harness classify the same command the same way.
	for _, verb := range CoderShellLiveVerbs() {
		if coderShellKindOfVerb[verb] != CoderShellCallLive {
			t.Fatalf("live verb %q not classified as live", verb)
		}
	}
	for _, verb := range CoderShellInteractiveVerbs() {
		if coderShellKindOfVerb[verb] != CoderShellCallInteract {
			t.Fatalf("interactive verb %q not classified as interactive", verb)
		}
	}
}
