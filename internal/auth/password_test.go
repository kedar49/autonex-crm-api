package auth

import "testing"

func TestHashPasswordRoundTrip(t *testing.T) {
	const pw = "correct horse battery staple"

	hash, err := HashPassword(pw)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}

	ok, err := VerifyPassword(pw, hash)
	if err != nil {
		t.Fatalf("VerifyPassword: %v", err)
	}
	if !ok {
		t.Fatal("expected correct password to verify")
	}

	ok, err = VerifyPassword("not the password", hash)
	if err != nil {
		t.Fatalf("VerifyPassword (wrong): %v", err)
	}
	if ok {
		t.Fatal("expected wrong password to fail")
	}
}

func TestHashPasswordIsSalted(t *testing.T) {
	a, err := HashPassword("same-password")
	if err != nil {
		t.Fatal(err)
	}
	b, err := HashPassword("same-password")
	if err != nil {
		t.Fatal(err)
	}
	if a == b {
		t.Fatal("expected distinct hashes for the same password (random salt)")
	}
}

func TestVerifyPasswordRejectsMalformedHash(t *testing.T) {
	if _, err := VerifyPassword("x", "not-a-phc-string"); err == nil {
		t.Fatal("expected error for malformed hash")
	}
}
