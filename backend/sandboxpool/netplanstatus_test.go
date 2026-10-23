package sandboxpool

import (
	"strings"
	"testing"
	"time"
)

func testStatusPolicy(maxWorkspaces int) NetworkPolicy {
	policy := NetworkPolicy{
		Mode:            NetworkModeShared,
		LoopbackAliases: true,
		PortBase:        12100,
		PortBlockSize:   4,
		MaxWorkspaces:   maxWorkspaces,
		LoopbackPrefix:  DefaultNetworkLoopbackPrefix,
		ReservedPorts:   append([]int(nil), DefaultReservedPorts...),
	}
	return policy
}

func TestNetworkPlanStatusRendersPolicy(t *testing.T) {
	t.Setenv(EnvNetworkMode, NetworkModeNetns)
	t.Setenv(EnvNetworkMaxWorkspaces, "5")
	policy := testStatusPolicy(5)
	policy.Mode = NetworkModeNetns
	policy.NetnsAvailable = true
	policy.IsolationRepos = []string{"acme/web"}

	alloc, err := NewPortAllocator(policy)
	if err != nil {
		t.Fatalf("NewPortAllocator: %v", err)
	}

	when := time.Date(2024, 5, 1, 12, 0, 0, 0, time.UTC)
	status := alloc.StatusAt(when)

	if status.GeneratedAt != "2024-05-01T12:00:00Z" {
		t.Errorf("GeneratedAt = %q", status.GeneratedAt)
	}
	if status.Mode != NetworkModeNetns || !status.NetnsAvailable {
		t.Errorf("mode/netns = %q/%v", status.Mode, status.NetnsAvailable)
	}
	if !status.LoopbackAliases || status.LoopbackPrefix != DefaultNetworkLoopbackPrefix {
		t.Errorf("aliases/prefix = %v/%q", status.LoopbackAliases, status.LoopbackPrefix)
	}
	if status.PortBase != 12100 || status.PortBlockSize != 4 {
		t.Errorf("block geometry = %d/%d", status.PortBase, status.PortBlockSize)
	}
	if status.MaxWorkspaces != 5 || status.Slots != 5 {
		t.Errorf("workspaces/slots = %d/%d", status.MaxWorkspaces, status.Slots)
	}
	if status.EnvMode != NetworkModeNetns || status.EnvMaxWorkspaces != "5" {
		t.Errorf("env levers = %q/%q", status.EnvMode, status.EnvMaxWorkspaces)
	}
	if len(status.IsolationRepos) != 1 || status.IsolationRepos[0] != "acme/web" {
		t.Errorf("isolation repos = %v", status.IsolationRepos)
	}

	// The plan exposes the block geometry the allocator enforces.
	plan, err := alloc.Allocate("11111111-1111-1111-1111-111111111111", "", "")
	if err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	status = alloc.Status()
	if status.ActiveWorkspaces != 1 || status.FreeSlots != 4 {
		t.Errorf("active/free = %d/%d", status.ActiveWorkspaces, status.FreeSlots)
	}
	if len(status.Workspaces) != 1 || status.Workspaces[0].WorkspaceUUID != plan.WorkspaceUUID {
		t.Fatalf("workspaces = %+v", status.Workspaces)
	}
	if got := status.PortGeometry; got != "[12100+4*5 => 12119]" {
		t.Errorf("PortGeometry = %q", got)
	}
	if status.Exhausted {
		t.Error("plan reported exhausted with a free slot")
	}
	if strings.Join(status.Notes, " ") == "" {
		t.Error("plan carries no notes")
	}
}

func TestNetworkPlanStatusReportsExhaustion(t *testing.T) {
	policy := testStatusPolicy(2)
	alloc, err := NewPortAllocator(policy)
	if err != nil {
		t.Fatalf("NewPortAllocator: %v", err)
	}
	if _, err := alloc.Allocate("11111111-1111-1111-1111-111111111111", "", ""); err != nil {
		t.Fatalf("Allocate 1: %v", err)
	}
	if _, err := alloc.Allocate("22222222-2222-2222-2222-222222222222", "", ""); err != nil {
		t.Fatalf("Allocate 2: %v", err)
	}

	status := alloc.Status()
	if !status.Exhausted || status.FreeSlots != 0 {
		t.Fatalf("exhausted/free = %v/%d", status.Exhausted, status.FreeSlots)
	}
	if !strings.Contains(strings.Join(status.Notes, " "), "ErrPoolNetworkCapacity") {
		t.Errorf("exhaustion note missing the sentinel: %v", status.Notes)
	}

	// The view is read-only: it must not have consumed or freed any block.
	if err := func() error {
		_, err := alloc.Allocate("33333333-3333-3333-3333-333333333333", "", "")
		return err
	}(); err == nil || !strings.Contains(err.Error(), "no free port block") {
		t.Errorf("third lease err = %v, want capacity failure", err)
	}

	// Sorted by block index so the operator view is stable.
	if !alloc.Release("11111111-1111-1111-1111-111111111111") {
		t.Fatal("Release failed")
	}
	if _, err := alloc.Allocate("33333333-3333-3333-3333-333333333333", "", ""); err != nil {
		t.Fatalf("Allocate after release: %v", err)
	}
	status = alloc.Status()
	if len(status.Workspaces) != 2 || status.Workspaces[0].BlockIndex >= status.Workspaces[1].BlockIndex {
		t.Errorf("workspaces not sorted by block index: %+v", status.Workspaces)
	}
}

func TestNetworkPlanStatusFromEnvInvalidModeFailsClosed(t *testing.T) {
	t.Setenv(EnvNetworkMode, "carrier-pigeon")

	status := NetworkPlanStatusFromEnv(time.Now())
	if status.PolicyError == "" {
		t.Fatal("invalid mode reported no PolicyError")
	}
	if !strings.Contains(status.PolicyError, ErrInvalidNetworkPolicy.Error()) {
		t.Errorf("PolicyError = %q", status.PolicyError)
	}
	if status.Slots != 0 || status.FreeSlots != 0 {
		t.Errorf("invalid policy still reported %d/%d slots", status.Slots, status.FreeSlots)
	}
	if len(status.Notes) == 0 {
		t.Error("invalid policy carries no remediation note")
	}
}

func TestNetworkPlanStatusFromEnvDefaults(t *testing.T) {
	t.Setenv(EnvNetworkMode, "")
	t.Setenv(EnvNetworkPortBase, "")
	t.Setenv(EnvNetworkMaxWorkspaces, "")

	status := NetworkPlanStatusFromEnv(time.Now())
	if status.PolicyError != "" {
		t.Fatalf("default env reported %q", status.PolicyError)
	}
	if status.Mode != NetworkModeShared || status.PortBase != DefaultNetworkPortBase {
		t.Errorf("defaults = %q/%d", status.Mode, status.PortBase)
	}
	if status.Slots != int(DefaultNetworkMaxWorkspaces) || status.FreeSlots != status.Slots {
		t.Errorf("default slots/free = %d/%d", status.Slots, status.FreeSlots)
	}
}

func TestResolveNetworkPlanStatusUsesAttachedAllocator(t *testing.T) {
	policy := testStatusPolicy(3)
	alloc, err := NewPortAllocator(policy)
	if err != nil {
		t.Fatalf("NewPortAllocator: %v", err)
	}
	if _, err := alloc.Allocate("44444444-4444-4444-4444-444444444444", "", ""); err != nil {
		t.Fatalf("Allocate: %v", err)
	}
	SetDefaultAllocator(alloc)
	defer func() { SetDefaultAllocator(nil) }()

	status := ResolveNetworkPlanStatus(time.Now())
	if status.ActiveWorkspaces != 1 || status.Slots != 3 {
		t.Errorf("attached allocator status = %+v", status)
	}
	if status.PolicyError != "" {
		t.Errorf("PolicyError = %q", status.PolicyError)
	}

	// A pool whose lease path never attached an allocator still reports the plan
	// the deployment env resolves to, marked as unattached.
	SetDefaultAllocator(nil)
	t.Setenv(EnvNetworkMaxWorkspaces, "0")
	unattached := ResolveNetworkPlanStatus(time.Now())
	if unattached.ActiveWorkspaces != 0 {
		t.Errorf("unattached status = %+v", unattached)
	}
	if !strings.Contains(strings.Join(unattached.Notes, " "), "no live allocator") {
		t.Errorf("unattached notes = %v", unattached.Notes)
	}
}
