package coordination

import "testing"

func TestReconcileFindsOneOwnerPerLane(t *testing.T) {
	report := Reconcile([]TaggedInteraction{
		{InteractionUUID: "b", Tags: []string{"repo:open-chat-go", "issue:747", "lane:wiring"}, Active: true},
		{InteractionUUID: "a", Tags: []string{"repo:open-chat-go", "issue:748", "lane:default"}, Active: true},
	})
	if !report.Clean() {
		t.Fatalf("expected a clean pass, got %+v", report)
	}
	if got := report.Owners["owner:747:wiring"]; got.InteractionUUID != "b" || !got.Active {
		t.Fatalf("unexpected owner for 747/wiring: %+v", got)
	}
	if got := report.Owners["owner:748:default"]; got.InteractionUUID != "a" {
		t.Fatalf("unexpected owner for 748/default: %+v", got)
	}
}

func TestReconcileIgnoresUnlanedInteractions(t *testing.T) {
	// Only interactions that opt into lane ownership are grouped: an un-laned
	// interaction is neither an owner nor a duplicate, and it is not
	// "unidentified" either - it simply does not take part in lane ownership.
	report := Reconcile([]TaggedInteraction{
		{InteractionUUID: "unlaned", Tags: []string{"repo:open-chat-go", "issue:747"}, Active: true},
		{InteractionUUID: "laned", Tags: []string{"issue:747", "lane:wiring"}, Active: true},
	})
	if !report.Clean() {
		t.Fatalf("an un-laned interaction must not be reported as a duplicate: %+v", report)
	}
	if len(report.Owners) != 1 || report.Owners["owner:747:wiring"].InteractionUUID != "laned" {
		t.Fatalf("unexpected owners: %+v", report.Owners)
	}
	if len(report.Unidentified) != 0 {
		t.Fatalf("a ticketed un-laned interaction is not unidentified: %+v", report.Unidentified)
	}
}

func TestReconcileUsesTheStampedOwnerKeyTag(t *testing.T) {
	// The dispatcher stamps the owner key it enforced, so reconcile must read
	// that key back verbatim - otherwise a lane could be reported as duplicated
	// (or missed) purely because the derivation drifted.
	report := Reconcile([]TaggedInteraction{
		{InteractionUUID: "a", Tags: []string{"issue:747", "owner:747:wiring"}, Active: true},
		{InteractionUUID: "b", Tags: []string{"issue:747", "owner:747:wiring"}, Active: true},
	})
	if len(report.Duplicates) != 1 || report.Duplicates[0].OwnerKey != "owner:747:wiring" {
		t.Fatalf("the stamped owner key must be used for grouping: %+v", report)
	}
}

func TestReconcileReportsDuplicateLaneAndKeepsTheLiveOwner(t *testing.T) {
	// The shape observed on the tracker: two interactions for one ticket, one of
	// them a stale entry that never released the lane (ci#744).
	report := Reconcile([]TaggedInteraction{
		{InteractionUUID: "stale", Tags: []string{"issue:744", "lane:wiring"}, Active: false},
		{InteractionUUID: "live", Tags: []string{"issue:744", "lane:wiring"}, Active: true},
	})
	if report.Clean() {
		t.Fatal("a lane owned twice must not be reported clean")
	}
	if len(report.Duplicates) != 1 {
		t.Fatalf("expected exactly one duplicate, got %+v", report.Duplicates)
	}
	dup := report.Duplicates[0]
	if dup.OwnerKey != "owner:744:wiring" {
		t.Fatalf("unexpected duplicate key %q", dup.OwnerKey)
	}
	if len(dup.Owners) != 2 || dup.Owners[0] != "live" || dup.Owners[1] != "stale" {
		t.Fatalf("duplicate owners must be sorted and complete: %+v", dup.Owners)
	}
	if owner := report.Owners["owner:744:wiring"]; owner.InteractionUUID != "live" || !owner.Active {
		t.Fatalf("the live interaction must keep the lane: %+v", owner)
	}
}

func TestReconcileIsDeterministic(t *testing.T) {
	interactions := []TaggedInteraction{
		{InteractionUUID: "z", Tags: []string{"issue:747", "lane:a"}, Active: true},
		{InteractionUUID: "y", Tags: []string{"issue:747", "lane:a"}, Active: true},
		{InteractionUUID: "x", Tags: []string{"no-ticket-tag"}, Active: true},
	}
	first := Reconcile(interactions)
	second := Reconcile(interactions)
	if first.Owners["owner:747:a"].InteractionUUID != second.Owners["owner:747:a"].InteractionUUID {
		t.Fatal("owner choice must not flap between passes")
	}
	if first.Owners["owner:747:a"].InteractionUUID != "y" {
		t.Fatalf("ties must resolve to the lowest uuid, got %+v", first.Owners["owner:747:a"])
	}
	if len(first.Unidentified) != 1 || first.Unidentified[0] != "x" {
		t.Fatalf("an interaction without a ticket tag must be reported unidentified: %+v", first.Unidentified)
	}
}

func TestReconcileSeparatesLanesOfTheSameTicket(t *testing.T) {
	report := Reconcile([]TaggedInteraction{
		{InteractionUUID: "wiring", Tags: []string{"issue:747", "lane:wiring"}, Active: true},
		{InteractionUUID: "canary", Tags: []string{"issue:747", "lane:canary"}, Active: true},
	})
	if !report.Clean() {
		t.Fatalf("different lanes of one ticket are not duplicates: %+v", report)
	}
	if len(report.Owners) != 2 {
		t.Fatalf("expected two owners, got %+v", report.Owners)
	}
}

func TestReconcileEmptyInputIsClean(t *testing.T) {
	if report := Reconcile(nil); !report.Clean() || len(report.Owners) != 0 {
		t.Fatalf("nil input must reconcile to a clean, empty report: %+v", report)
	}
}
