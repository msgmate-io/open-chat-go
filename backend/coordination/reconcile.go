package coordination

import "sort"

// TaggedInteraction is the minimal view of an in-flight interaction the
// reconcile step needs: its identity and its tags (ci#747 RC7/QW4).
type TaggedInteraction struct {
	// InteractionUUID identifies the interaction (chat uuid).
	InteractionUUID string
	// Tags are the interaction's tags, e.g. {"repo:open-chat-go","issue:747"}.
	Tags []string
	// Active reports whether the interaction is still doing work. A terminal
	// interaction releases its lane; an active one owns it.
	Active bool
}

// Owner is one reconciled unit of coordination work.
type Owner struct {
	Ticket          string
	Lane            string
	OwnerKey        string
	InteractionUUID string
	Active          bool
}

// Duplicate reports one lane that is owned by more than one interaction.
type Duplicate struct {
	Ticket   string
	Lane     string
	OwnerKey string
	// Owners are the interaction uuids competing for the lane, sorted so the
	// report is stable across runs.
	Owners []string
}

// ReconcileReport is the outcome of one reconcile pass.
type ReconcileReport struct {
	// Owners maps an owner key to the single interaction that holds it.
	Owners map[string]Owner
	// Duplicates lists every lane with more than one interaction, including the
	// case where one of them is already terminal (a stale entry that never
	// released its lane).
	Duplicates []Duplicate
	// Unidentified lists interactions whose tags name no unit of work. They
	// cannot be de-duplicated, so reconcile reports them instead of guessing.
	Unidentified []string
}

// Clean reports whether the pass found nothing to fix.
func (r ReconcileReport) Clean() bool {
	return len(r.Duplicates) == 0 && len(r.Unidentified) == 0
}

// Reconcile maps interactions onto owner keys and reports the collisions that
// the dispatcher's (ticket, lane) dedupe key must prevent (ci#747 RC7/QW4).
//
// It is intentionally pure: the same input always yields the same report, so the
// reconcile step of every run is assertable in a unit test and in the canary.
func Reconcile(interactions []TaggedInteraction) ReconcileReport {
	report := ReconcileReport{Owners: map[string]Owner{}}
	grouped := map[string][]TaggedInteraction{}

	for _, interaction := range interactions {
		key := OwnerKeyFromTags(interaction.Tags)
		if key == "" {
			// Only interactions that participate in lane ownership are grouped:
			// an un-laned interaction is neither an owner nor a duplicate, so it
			// must not be reported as either.
			if _, ticket := TicketFromTags(interaction.Tags); ticket == "" {
				report.Unidentified = append(report.Unidentified, interaction.InteractionUUID)
			}
			continue
		}
		grouped[key] = append(grouped[key], interaction)
	}

	keys := make([]string, 0, len(grouped))
	for key := range grouped {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	for _, key := range keys {
		members := grouped[key]
		owner := pickOwner(members)
		report.Owners[key] = owner
		if len(members) > 1 {
			owners := make([]string, 0, len(members))
			for _, member := range members {
				owners = append(owners, member.InteractionUUID)
			}
			sort.Strings(owners)
			report.Duplicates = append(report.Duplicates, Duplicate{
				Ticket:   owner.Ticket,
				Lane:     owner.Lane,
				OwnerKey: key,
				Owners:   owners,
			})
		}
	}

	sort.Strings(report.Unidentified)
	return report
}

// pickOwner chooses the interaction that keeps the lane: a live interaction
// always wins over a terminal one, and among equals the lowest uuid wins, so the
// choice is deterministic and the reconcile step does not flap between runs.
func pickOwner(members []TaggedInteraction) Owner {
	best := members[0]
	for _, member := range members[1:] {
		if betterOwner(member, best) {
			best = member
		}
	}
	key := OwnerKeyFromTags(best.Tags)
	ticket, lane := TicketFromTags(best.Tags)
	return Owner{
		Ticket:          ticket,
		Lane:            lane,
		OwnerKey:        key,
		InteractionUUID: best.InteractionUUID,
		Active:          best.Active,
	}
}

func betterOwner(candidate, current TaggedInteraction) bool {
	if candidate.Active != current.Active {
		return candidate.Active
	}
	return candidate.InteractionUUID < current.InteractionUUID
}
