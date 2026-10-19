package nativecoder

import "strings"

// This file holds the small, side-effect free string canonicalisers that are
// shared by every coder sub-surface (interaction tags, the development-lead
// router, the auto-consolidation planner). They used to live in the file of
// whichever caller happened to need them first, which is how `main` ended up
// with two competing `NormalizeRepo` implementations (ci#650 vs ci#608) and a
// package that no longer compiled. Keeping them here gives every caller one
// canonical implementation to import.
//
// The canonicalisers are pure: they take a raw value and return its canonical
// form, with no I/O and no dependency on the rest of the package.

// NormalizeBranch lower-cases a branch and strips the `refs/heads/` prefix.
func NormalizeBranch(branch string) string {
	normalized := strings.ToLower(strings.TrimSpace(branch))
	return strings.TrimPrefix(normalized, "refs/heads/")
}

// NormalizeRepo turns a repository reference (an `owner/name` slug, a clone URL
// or an `git@host:owner/name` remote) into the bare `owner/name` used in tags
// and routing decisions.
// It returns "" when the value carries no usable repository, so callers can use
// it both to canonicalise and to validate that a repo was supplied at all.
func NormalizeRepo(value string) string {
	repo := strings.TrimSpace(value)
	if repo == "" {
		return ""
	}
	repo = strings.TrimSuffix(repo, ".git")
	if idx := strings.Index(repo, "://"); idx >= 0 {
		repo = repo[idx+len("://"):]
		// drop the host part of a URL clone target
		if slash := strings.Index(repo, "/"); slash >= 0 {
			repo = repo[slash+1:]
		}
	} else if idx := strings.Index(repo, "@"); idx >= 0 {
		repo = repo[idx+1:]
		if colon := strings.Index(repo, ":"); colon >= 0 {
			repo = repo[colon+1:]
		}
	}
	repo = strings.Trim(repo, "/")
	parts := strings.Split(repo, "/")
	if len(parts) < 2 {
		return ""
	}
	owner := slugify(parts[len(parts)-2])
	name := slugify(parts[len(parts)-1])
	if owner == "" || name == "" {
		return ""
	}
	return owner + "/" + name
}

// slugify lowercases a label and collapses every run of non-alphanumeric
// characters into a single dash, so tags stay stable and comparable.
func slugify(value string) string {
	var b strings.Builder
	lastDash := false
	for _, r := range strings.ToLower(strings.TrimSpace(value)) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
			lastDash = false
		default:
			if !lastDash && b.Len() > 0 {
				b.WriteByte('-')
				lastDash = true
			}
		}
	}
	return strings.Trim(b.String(), "-")
}
