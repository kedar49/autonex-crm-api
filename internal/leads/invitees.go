package leads

import "strings"

// invitees returns the addresses to put on the calendar invite.
//
// The list is honoured only when InviteConfirmed is set, which is the
// human-in-the-loop guarantee: the server never mails a customer unless a person
// saw the list and pressed the button. A client that pre-fills attendees without
// asking — a bug, or a future caller that forgets — sends nothing.
//
// Addresses are trimmed, lower-cased, deduplicated and sanity-checked, because
// each one becomes a real email from your domain to somebody's inbox.
func invitees(adv Advance) []string {
	if !adv.InviteConfirmed || len(adv.Attendees) == 0 {
		return nil
	}

	seen := make(map[string]bool, len(adv.Attendees))
	out := make([]string, 0, len(adv.Attendees))
	for _, raw := range adv.Attendees {
		email := strings.ToLower(strings.TrimSpace(raw))
		if !plausibleEmail(email) || seen[email] {
			continue
		}
		seen[email] = true
		out = append(out, email)
	}
	return out
}

// plausibleEmail is a shape check, not validation: Google rejects a malformed
// attendee and fails the whole booking, so a typo in one address must not cost
// the meeting.
func plausibleEmail(s string) bool {
	at := strings.LastIndex(s, "@")
	if at <= 0 || at == len(s)-1 {
		return false
	}
	return strings.Contains(s[at+1:], ".")
}
