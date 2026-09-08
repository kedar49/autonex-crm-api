package leads

import "testing"

func adv(attendees []string, confirmed bool) Advance {
	return Advance{ToStage: "call scheduled", Attendees: attendees, InviteConfirmed: confirmed}
}

// The guard the whole human-in-the-loop design rests on: an address is only
// emailed when a person explicitly confirmed it in the dialog. A client bug that
// pre-fills attendees must not be able to mail a customer on its own.
func TestAttendeesAreIgnoredWithoutConfirmation(t *testing.T) {
	got := invitees(adv([]string{"lead@example.com"}, false))
	if len(got) != 0 {
		t.Errorf("invitees = %v, want none — the invite was never confirmed", got)
	}
}

func TestConfirmedAttendeesAreReturned(t *testing.T) {
	got := invitees(adv([]string{"lead@example.com", "contact@example.com"}, true))
	if len(got) != 2 || got[0] != "lead@example.com" || got[1] != "contact@example.com" {
		t.Errorf("invitees = %v, want both addresses", got)
	}
}

// Confirming an empty list is a booking with no invites, not an error.
func TestConfirmationWithoutAttendeesInvitesNobody(t *testing.T) {
	if got := invitees(adv(nil, true)); len(got) != 0 {
		t.Errorf("invitees = %v, want none", got)
	}
}

func TestInviteesAreCleanedAndDeduplicated(t *testing.T) {
	got := invitees(adv([]string{
		"  Lead@Example.com ",
		"lead@example.com",
		"",
		"   ",
		"not-an-email",
		"contact@example.com",
	}, true))

	want := []string{"lead@example.com", "contact@example.com"}
	if len(got) != len(want) {
		t.Fatalf("invitees = %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("invitees[%d] = %q, want %q", i, got[i], want[i])
		}
	}
}
