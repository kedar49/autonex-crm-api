package middleware

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// serveWithRole runs a request with role already in context (as RequireJWT
// would have put it there) through RequireRole, reporting the status and
// whether the guarded handler ran.
func serveWithRole(t *testing.T, role string, allowed ...string) (status int, ran bool) {
	t.Helper()

	next := http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		ran = true
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	ctx := context.WithValue(req.Context(), roleKey, role)
	req = req.WithContext(ctx)

	rec := httptest.NewRecorder()
	RequireRole(allowed...)(next).ServeHTTP(rec, req)

	return rec.Code, ran
}

func TestRequireRoleAllowsListedRole(t *testing.T) {
	status, ran := serveWithRole(t, "admin", "owner", "admin", "account_manager")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if !ran {
		t.Fatal("handler did not run for an allowed role")
	}
}

func TestRequireRoleRejectsUnlistedRole(t *testing.T) {
	status, ran := serveWithRole(t, "sales", "owner", "admin", "account_manager")
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", status)
	}
	if ran {
		t.Fatal("handler ran for a disallowed role")
	}
}

func TestRequireRoleRejectsMissingRole(t *testing.T) {
	// A token minted before roles existed, or by a path that never looked one
	// up, carries no role claim — RequireRole must fail closed, not open.
	status, ran := serveWithRole(t, "", "owner", "admin", "account_manager")
	if status != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", status)
	}
	if ran {
		t.Fatal("handler ran with no role")
	}
}
