package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

const testSecret = "test-secret"

// sign mints a token with the given claims, signed with method and key.
func sign(t *testing.T, method jwt.SigningMethod, key any, claims jwt.MapClaims) string {
	t.Helper()
	tok, err := jwt.NewWithClaims(method, claims).SignedString(key)
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	return tok
}

func validClaims() jwt.MapClaims {
	return jwt.MapClaims{
		"sub": "user-1",
		"org": "org-1",
		"exp": time.Now().Add(time.Minute).Unix(),
	}
}

// serve runs a request with the given Authorization header through RequireJWT,
// reporting the status and what the guarded handler saw.
func serve(t *testing.T, authHeader string) (status int, userID, orgID string) {
	t.Helper()

	next := http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		userID, orgID = UserID(r.Context()), OrgID(r.Context())
	})

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	rec := httptest.NewRecorder()
	RequireJWT(testSecret)(next).ServeHTTP(rec, req)

	return rec.Code, userID, orgID
}

func TestRequireJWTAcceptsValidToken(t *testing.T) {
	tok := sign(t, jwt.SigningMethodHS256, []byte(testSecret), validClaims())

	status, userID, orgID := serve(t, "Bearer "+tok)
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200", status)
	}
	if userID != "user-1" {
		t.Errorf("UserID = %q, want user-1", userID)
	}
	// Handlers scope every query by this; an empty value would widen a query to
	// every tenant.
	if orgID != "org-1" {
		t.Errorf("OrgID = %q, want org-1", orgID)
	}
}

func TestRequireJWTRejects(t *testing.T) {
	noOrg := validClaims()
	delete(noOrg, "org")

	emptyOrg := validClaims()
	emptyOrg["org"] = ""

	noSub := validClaims()
	delete(noSub, "sub")

	expired := validClaims()
	expired["exp"] = time.Now().Add(-time.Minute).Unix()

	tests := map[string]string{
		"no header":       "",
		"empty bearer":    "Bearer ",
		"malformed token": "Bearer abc.def.ghi",
		// Pre-tenancy tokens: valid signature, but they can't name a tenant.
		"missing org claim": "Bearer " + sign(t, jwt.SigningMethodHS256, []byte(testSecret), noOrg),
		"empty org claim":   "Bearer " + sign(t, jwt.SigningMethodHS256, []byte(testSecret), emptyOrg),
		"missing subject":   "Bearer " + sign(t, jwt.SigningMethodHS256, []byte(testSecret), noSub),
		"expired":           "Bearer " + sign(t, jwt.SigningMethodHS256, []byte(testSecret), expired),
		"wrong secret":      "Bearer " + sign(t, jwt.SigningMethodHS256, []byte("other-secret"), validClaims()),
		// Classic alg-confusion attempt.
		"alg none": "Bearer " + sign(t, jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, validClaims()),
	}

	for name, header := range tests {
		t.Run(name, func(t *testing.T) {
			status, userID, orgID := serve(t, header)
			if status != http.StatusUnauthorized {
				t.Fatalf("status = %d, want 401", status)
			}
			if userID != "" || orgID != "" {
				t.Fatalf("handler ran with userID=%q orgID=%q; it should not run at all", userID, orgID)
			}
		})
	}
}
