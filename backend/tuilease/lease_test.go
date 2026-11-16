package tuilease

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func baseTime() time.Time { return time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC) }

func leaseOn(chat, session string) Lease {
	return Lease{ChatUUID: chat, InteractionUUID: "interaction-1", SessionID: session, SandboxUUID: "sandbox-1", Transport: TransportSSH}
}

func TestHeartbeatRegistersAndRenews(t *testing.T) {
	reg := NewRegistry(time.Minute)
	now := baseTime()
	if err := reg.Heartbeat(leaseOn("chat-1", "sess-1"), now); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}

	// A heartbeat inside the TTL keeps the lease alive past the original
	// deadline: a TUI that is merely slow must not be reported as detached.
	if err := reg.Heartbeat(leaseOn("chat-1", "sess-1"), now.Add(45*time.Second)); err != nil {
		t.Fatalf("renew: %v", err)
	}
	if expired := reg.Expired(now.Add(75 * time.Second)); len(expired) != 0 {
		t.Fatalf("renewed lease must not expire, got %+v", expired)
	}
	if active := reg.ActiveLeases("chat-1"); len(active) != 1 || active[0].SandboxUUID != "sandbox-1" {
		t.Fatalf("unexpected active leases: %+v", active)
	}
}

func TestExpiredForgetsTheLease(t *testing.T) {
	reg := NewRegistry(time.Minute)
	now := baseTime()
	if err := reg.Heartbeat(leaseOn("chat-1", "sess-1"), now); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}

	expired := reg.Expired(now.Add(2 * time.Minute))
	if len(expired) != 1 || expired[0].SessionID != "sess-1" {
		t.Fatalf("want the dead lease reported once, got %+v", expired)
	}
	if again := reg.Expired(now.Add(2 * time.Minute)); len(again) != 0 {
		t.Fatalf("an expired lease must be reported only once, got %+v", again)
	}
}

func TestExpiredIsDeterministic(t *testing.T) {
	reg := NewRegistry(time.Minute)
	now := baseTime()
	for _, session := range []string{"sess-b", "sess-a"} {
		if err := reg.Heartbeat(leaseOn("chat-1", session), now); err != nil {
			t.Fatalf("heartbeat: %v", err)
		}
	}
	expired := reg.Expired(now.Add(5 * time.Minute))
	if len(expired) != 2 || expired[0].SessionID != "sess-a" || expired[1].SessionID != "sess-b" {
		t.Fatalf("expired leases must be sorted, got %+v", expired)
	}
}

func TestTwoSessionsOfOneChatAreIndependent(t *testing.T) {
	reg := NewRegistry(time.Minute)
	now := baseTime()
	if err := reg.Heartbeat(leaseOn("chat-1", "sess-a"), now); err != nil {
		t.Fatalf("heartbeat a: %v", err)
	}
	if err := reg.Heartbeat(leaseOn("chat-1", "sess-b"), now); err != nil {
		t.Fatalf("heartbeat b: %v", err)
	}
	if err := reg.Heartbeat(leaseOn("chat-1", "sess-b"), now.Add(30*time.Second)); err != nil {
		t.Fatalf("renew b: %v", err)
	}

	expired := reg.Expired(now.Add(70 * time.Second))
	if len(expired) != 1 || expired[0].SessionID != "sess-a" {
		t.Fatalf("only the silent session may expire, got %+v", expired)
	}
}

func TestDetachRemovesLeaseSoItIsNotReportedTwice(t *testing.T) {
	reg := NewRegistry(time.Minute)
	now := baseTime()
	if err := reg.Heartbeat(leaseOn("chat-1", "sess-1"), now); err != nil {
		t.Fatalf("heartbeat: %v", err)
	}
	if _, ok := reg.Detach("chat-1", "sess-1"); !ok {
		t.Fatal("a clean detach must find the lease")
	}
	if expired := reg.Expired(now.Add(time.Hour)); len(expired) != 0 {
		t.Fatalf("a detached lease must not be reported again, got %+v", expired)
	}
	// Detaching an unknown session (killed TUI) is not an error.
	if _, ok := reg.Detach("chat-1", "gone"); ok {
		t.Fatal("detaching an unknown lease must report absence")
	}
}

func TestHeartbeatValidatesTheLease(t *testing.T) {
	reg := NewRegistry(time.Minute)
	cases := []Lease{
		{SessionID: "sess-1"},
		{ChatUUID: "chat-1"},
		{ChatUUID: "   ", SessionID: "  "},
	}
	for i, l := range cases {
		if err := reg.Heartbeat(l, baseTime()); err == nil {
			t.Fatalf("case %d: want an invalid-lease error", i)
		}
	}
	var nilReg *Registry
	if err := nilReg.Heartbeat(leaseOn("chat-1", "sess-1"), baseTime()); err == nil {
		t.Fatal("a nil registry must reject heartbeats")
	}
}

func TestReportDefaultsToTheSandboxTheInteractionRanIn(t *testing.T) {
	lease := leaseOn("chat-1", "sess-1")
	report := NewReport(lease, baseTime(), []Candidate{
		{SandboxUUID: "sandbox-other", Provider: "k8s", Running: true},
		{SandboxUUID: "sandbox-1", Provider: "k8s"},
		{SandboxUUID: "  "}, // dropped: no identifier
	})
	if got := report.DefaultSandboxUUID(); got != "sandbox-1" {
		t.Fatalf("default sandbox = %q, want sandbox-1", got)
	}
	if len(report.Candidates) != 2 {
		t.Fatalf("candidates without a sandbox uuid must be dropped, got %+v", report.Candidates)
	}
	if report.DetachedReason() != ReasonTUIdetached {
		t.Fatalf("reason = %q", report.DetachedReason())
	}
	if report.Transport != TransportSSH {
		t.Fatalf("transport = %q", report.Transport)
	}
}

func TestReportFallsBackToARunningThenFirstCandidate(t *testing.T) {
	lease := Lease{ChatUUID: "chat-1", SessionID: "sess-1", SandboxUUID: "sandbox-gone"}
	report := NewReport(lease, baseTime(), []Candidate{
		{SandboxUUID: "sandbox-a"},
		{SandboxUUID: "sandbox-b", Running: true},
	})
	if got := report.DefaultSandboxUUID(); got != "sandbox-b" {
		t.Fatalf("default = %q, want the running sandbox", got)
	}

	report = NewReport(lease, baseTime(), []Candidate{{SandboxUUID: "sandbox-a"}, {SandboxUUID: "sandbox-b"}})
	if got := report.DefaultSandboxUUID(); got != "sandbox-a" {
		t.Fatalf("default = %q, want the first candidate", got)
	}

	empty := NewReport(lease, baseTime(), nil)
	if got := empty.DefaultSandboxUUID(); got != "" {
		t.Fatalf("default = %q, want none", got)
	}
}

func TestReportMetadataMarksTheTurnFailedAndOmitsSecrets(t *testing.T) {
	report := NewReport(leaseOn("chat-1", "sess-1"), baseTime(), []Candidate{
		{SandboxUUID: "sandbox-1", Provider: "k8s", Label: "chat sandbox", Running: true, ExpiresAt: baseTime().Add(time.Hour)},
	})
	meta := report.Metadata()
	if meta["finished"] != true || meta["error"] != true {
		t.Fatalf("the detached turn must be a finished failure: %+v", meta)
	}
	if meta["failure_reason"] != ReasonTUIdetached {
		t.Fatalf("failure_reason = %v", meta["failure_reason"])
	}
	payload, ok := meta[MetadataKey].(map[string]interface{})
	if !ok {
		t.Fatalf("metadata has no %q block: %+v", MetadataKey, meta)
	}
	if payload["default_sandbox"] != "sandbox-1" || payload["transport"] != TransportSSH {
		t.Fatalf("unexpected payload: %+v", payload)
	}
	candidates, ok := payload["candidates"].([]map[string]interface{})
	if !ok || len(candidates) != 1 || candidates[0]["sandbox_uuid"] != "sandbox-1" {
		t.Fatalf("unexpected candidates: %+v", payload["candidates"])
	}

	// The metadata the browser receives must never carry credentials or user data:
	// exact-match the serialised form for the words that would indicate a leak.
	encoded, err := json.Marshal(meta)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	for _, forbidden := range []string{"token", "kubeconfig", "password", "secret", "owner_uuid", "username"} {
		if strings.Contains(strings.ToLower(string(encoded)), forbidden) {
			t.Fatalf("metadata leaks %q: %s", forbidden, encoded)
		}
	}
}

func TestReportTextNamesTheDetachAndTheCandidates(t *testing.T) {
	report := NewReport(leaseOn("chat-1", "sess-1"), baseTime(), []Candidate{
		{SandboxUUID: "sandbox-1", Label: "chat sandbox"},
	})
	text := report.Text()
	for _, want := range []string{"local ssh link", "sandbox-1", "chat sandbox", "Continue it"} {
		if !strings.Contains(text, want) {
			t.Fatalf("text %q does not contain %q", text, want)
		}
	}

	bare := NewReport(leaseOn("chat-1", "sess-1"), baseTime(), nil)
	if text := bare.Text(); !strings.Contains(text, "No upstream sandbox") {
		t.Fatalf("a report without candidates must say so, got %q", text)
	}
}

func TestContinueRequestSelectsAnOfferedSandboxOnly(t *testing.T) {
	report := NewReport(leaseOn("chat-1", "sess-1"), baseTime(), []Candidate{
		{SandboxUUID: "sandbox-1", Provider: "k8s"},
		{SandboxUUID: "sandbox-2", Provider: "k8s", Running: true},
	})

	selected, err := report.ContinueRequest("sandbox-2")
	if err != nil {
		t.Fatalf("continue: %v", err)
	}
	if !selected.Resume || selected.SandboxUUID != "sandbox-2" || selected.ChatUUID != "chat-1" || selected.Provider != "k8s" {
		t.Fatalf("unexpected continue request: %+v", selected)
	}

	// An empty selection takes the report default, which prefers the sandbox the
	// interaction ran in.
	def, err := report.ContinueRequest("")
	if err != nil {
		t.Fatalf("default continue: %v", err)
	}
	if def.SandboxUUID != "sandbox-1" {
		t.Fatalf("default continue picked %q", def.SandboxUUID)
	}

	if _, err := report.ContinueRequest("sandbox-not-offered"); err == nil {
		t.Fatal("continuing in a sandbox the operator was not offered must fail")
	}

	empty := NewReport(leaseOn("chat-1", "sess-1"), baseTime(), nil)
	if _, err := empty.ContinueRequest(""); err == nil {
		t.Fatal("continuing without any candidate must fail")
	}
}
