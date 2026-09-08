package auth

import (
	"context"
	"errors"
	"testing"

	"github.com/go-crm/services/pkg/config"
)

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

// Regression: the allow-list used to be enforced only on the SSO path, so
// password signup walked straight past it and anyone could create an account
// and a workspace on any domain.
//
// The check runs before the service touches its store, so a nil store is enough
// to prove it: if the guard is ever moved below the lookup this panics instead
// of returning, which is exactly the failure worth catching.
func TestRegisterRejectsDisallowedDomainBeforeTouchingTheStore(t *testing.T) {
	svc := &Service{
		store: &store{pool: nil},
		cfg:   config.Config{SSOAllowedDomains: []string{"autonexai360.com"}},
	}

	_, err := svc.Register(context.Background(), "outsider@gmail.com", "hunter2-long-enough", "Outsider")
	if !errors.Is(err, ErrDomainNotAllowed) {
		t.Fatalf("err = %v, want ErrDomainNotAllowed", err)
	}
}

// The address is normalized before the check, so casing and stray whitespace
// cannot smuggle a domain past it.
func TestRegisterNormalizesEmailBeforeCheckingTheDomain(t *testing.T) {
	svc := &Service{
		store: &store{pool: nil},
		cfg:   config.Config{SSOAllowedDomains: []string{"autonexai360.com"}},
	}

	_, err := svc.Register(context.Background(), "  OUTSIDER@GMAIL.COM ", "hunter2-long-enough", "")
	if !errors.Is(err, ErrDomainNotAllowed) {
		t.Fatalf("err = %v, want ErrDomainNotAllowed", err)
	}
}
