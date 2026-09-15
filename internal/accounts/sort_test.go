package accounts

import (
	"strings"
	"testing"
)

// The sort key selects a constant clause; it is never interpolated. These tests
// guard that property, which is what keeps the parameter out of SQL.
func TestValidSortAcceptsOnlyKnownKeys(t *testing.T) {
	for _, key := range []string{"name", "nameDesc", "created", "createdAsc", "updated"} {
		if !ValidSort(key) {
			t.Errorf("ValidSort(%q) = false, want true", key)
		}
	}
	for _, key := range []string{"", "NAME", "a.name", "name; DROP TABLE accounts", "random"} {
		if ValidSort(key) {
			t.Errorf("ValidSort(%q) = true, want false", key)
		}
	}
}

func TestOrderForFallsBackToDefault(t *testing.T) {
	for _, key := range []string{"", "bogus", "name); DELETE FROM accounts --"} {
		if got := orderFor(key); got != defaultOrder {
			t.Errorf("orderFor(%q) = %q, want the default order", key, got)
		}
	}
}

func TestOrderForNeverEchoesTheKey(t *testing.T) {
	const injection = "name UNION SELECT 1"
	if strings.Contains(orderFor(injection), injection) {
		t.Fatal("orderFor echoed its argument into the clause")
	}
}
