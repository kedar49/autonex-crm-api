package auth

import "testing"

func TestDomainAllowedWithNoListPermitsEverything(t *testing.T) {
	// A deployment that has not set SSO_ALLOWED_DOMAINS must behave as it always
	// did; the guard is opt-in.
	for _, email := range []string{"a@example.com", "b@autonexai360.com", ""} {
		if !domainAllowed(nil, email) {
			t.Errorf("domainAllowed(nil, %q) = false, want true", email)
		}
	}
}

func TestDomainAllowedMatchesOnlyTheListedDomains(t *testing.T) {
	allowed := []string{"autonexai360.com"}

	for _, email := range []string{
		"karan.paigude@autonexai360.com",
		"karan.paigude+sales@autonexai360.com",
	} {
		if !domainAllowed(allowed, email) {
			t.Errorf("domainAllowed(%q) = false, want true", email)
		}
	}

	for _, email := range []string{
		"someone@gmail.com",
		// A different domain that merely ends with the allowed one must not pass.
		"attacker@evil-autonexai360.com",
		// Nor one that has it as a prefix.
		"someone@autonexai360.com.evil.net",
		// The gtempaccount address Google showed during setup is a different
		// domain and must be refused.
		"autonex.tech%autonexai360.com@gtempaccount.com",
		"no-at-sign",
		"trailing@",
		"",
	} {
		if domainAllowed(allowed, email) {
			t.Errorf("domainAllowed(%q) = true, want false", email)
		}
	}
}

func TestDomainAllowedAcceptsSeveralDomains(t *testing.T) {
	allowed := []string{"autonexai360.com", "autonex.com"}
	for _, email := range []string{"a@autonexai360.com", "b@autonex.com"} {
		if !domainAllowed(allowed, email) {
			t.Errorf("domainAllowed(%q) = false, want true", email)
		}
	}
	if domainAllowed(allowed, "c@autonex.tech") {
		t.Error("autonex.tech is not on the list and must be refused")
	}
}
