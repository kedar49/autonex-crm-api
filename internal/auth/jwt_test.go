package auth

import (
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"

	"github.com/Autonex009/autonex-crm-api/pkg/config"
)

func TestIssueAccessToken(t *testing.T) {
	cfg := config.Config{
		JWTSecret:    "test-secret",
		JWTIssuer:    "go-crm-test",
		JWTAccessTTL: time.Minute,
	}

	tok, err := issueAccessToken(cfg, User{ID: "user-123", Email: "a@b.com", OrgID: "org-789", Role: "admin"})
	if err != nil {
		t.Fatalf("issueAccessToken: %v", err)
	}

	parsed, err := jwt.Parse(tok, func(*jwt.Token) (interface{}, error) {
		return []byte(cfg.JWTSecret), nil
	}, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil || !parsed.Valid {
		t.Fatalf("parse issued token: %v", err)
	}

	sub, _ := parsed.Claims.GetSubject()
	if sub != "user-123" {
		t.Fatalf("subject = %q, want user-123", sub)
	}
	iss, _ := parsed.Claims.GetIssuer()
	if iss != "go-crm-test" {
		t.Fatalf("issuer = %q, want go-crm-test", iss)
	}
	// Every CRM query is scoped by this claim, so its absence would be a
	// tenant-isolation bug, not a cosmetic one.
	claims, _ := parsed.Claims.(jwt.MapClaims)
	if org, _ := claims["org"].(string); org != "org-789" {
		t.Fatalf("org claim = %q, want org-789", org)
	}
	// RequireRole reads this claim rather than hitting the DB per request, so
	// its absence would silently deny every role-gated route.
	if role, _ := claims["role"].(string); role != "admin" {
		t.Fatalf("role claim = %q, want admin", role)
	}
}

func TestIssuedTokenRejectsWrongSecret(t *testing.T) {
	cfg := config.Config{JWTSecret: "right", JWTIssuer: "go-crm", JWTAccessTTL: time.Minute}

	tok, err := issueAccessToken(cfg, User{ID: "u1", Email: "a@b.com", OrgID: "o1"})
	if err != nil {
		t.Fatal(err)
	}

	_, err = jwt.Parse(tok, func(*jwt.Token) (interface{}, error) {
		return []byte("wrong"), nil
	}, jwt.WithValidMethods([]string{"HS256"}))
	if err == nil {
		t.Fatal("expected verification to fail with wrong secret")
	}
}
