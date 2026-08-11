package activities

import "testing"

// A note typed by a person must not be stored as a system event: system rows
// render in the quieter style and the update/delete SQL excludes them, so
// misfiling one makes the user's own words permanently uneditable.
func TestResolveKindKeepsHumanEntriesHuman(t *testing.T) {
	for _, kind := range Kinds {
		if got := resolveKind(kind); got != kind {
			t.Errorf("resolveKind(%q) = %q, want %q", kind, got, kind)
		}
	}
}

func TestResolveKindFallsBackToSystem(t *testing.T) {
	// Empty is the common case (most call sites are genuine system events);
	// "system" and junk both have to survive without violating the CHECK.
	for _, kind := range []string{"", "system", "not-a-kind", "NOTE"} {
		if got := resolveKind(kind); got != "system" {
			t.Errorf("resolveKind(%q) = %q, want system", kind, got)
		}
	}
}
