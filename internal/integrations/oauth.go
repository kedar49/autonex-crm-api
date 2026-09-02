package integrations

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/oauth2"

	"github.com/go-crm/services/pkg/config"
)

// calendarScope is the narrowest scope that can create an event with a Meet.
// Deliberately not calendar (full read/write of every calendar) — this feature
// books meetings, it has no business reading someone's diary.
const calendarScope = "https://www.googleapis.com/auth/calendar.events"

const userInfoURL = "https://openidconnect.googleapis.com/v1/userinfo"

// stateTTL bounds how long a consent may sit unfinished before the callback
// refuses it.
const stateTTL = 10 * time.Minute

// googleConfig builds the OAuth client for the calendar connection. It reuses
// the same Google credentials as SSO login but a different redirect URI, so both
// must be registered in the Google console.
func googleConfig(cfg config.Config) (*oauth2.Config, error) {
	creds, ok := cfg.OAuthCreds["google"]
	if !ok || creds.ClientID == "" {
		return nil, errors.New("google is not configured (set GOOGLE_CLIENT_ID)")
	}
	return &oauth2.Config{
		ClientID:     creds.ClientID,
		ClientSecret: creds.ClientSecret,
		Endpoint: oauth2.Endpoint{
			AuthURL:  "https://accounts.google.com/o/oauth2/v2/auth",
			TokenURL: "https://oauth2.googleapis.com/token",
		},
		RedirectURL: strings.TrimRight(cfg.IntegrationsRedirectBase, "/") + "/google/callback",
		Scopes:      []string{calendarScope, "openid", "email"},
	}, nil
}

// authCodeURL asks for offline access and forces the consent screen.
//
// Google issues a refresh token only on the first consent for a given client and
// user. Without prompt=consent a re-connection returns an access token alone,
// and the integration would silently stop working an hour later with no way to
// renew it.
func authCodeURL(oc *oauth2.Config, state string) string {
	return oc.AuthCodeURL(state,
		oauth2.AccessTypeOffline,
		oauth2.SetAuthURLParam("prompt", "consent"),
		oauth2.SetAuthURLParam("include_granted_scopes", "true"),
	)
}

// signState returns an opaque state carrying the user id and a nonce, signed
// with the app's JWT secret.
//
// The user id has to survive the round trip because the callback is a top-level
// browser navigation with no Authorization header. Signing it means the callback
// can trust it without server-side session storage; the nonce is echoed in a
// cookie so a stolen link alone cannot complete a connection.
func signState(secret, userID, nonce string) string {
	payload := fmt.Sprintf("%s.%s.%d", userID, nonce, time.Now().Add(stateTTL).Unix())
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString([]byte(payload)) + "." +
		base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// parseState verifies the signature and expiry and returns the user id and nonce.
func parseState(secret, state string) (userID, nonce string, err error) {
	parts := strings.Split(state, ".")
	if len(parts) != 2 {
		return "", "", errors.New("malformed state")
	}
	raw, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", "", errors.New("malformed state")
	}
	sig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", "", errors.New("malformed state")
	}

	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(raw)
	if !hmac.Equal(sig, mac.Sum(nil)) {
		return "", "", errors.New("state signature does not verify")
	}

	fields := strings.Split(string(raw), ".")
	if len(fields) != 3 {
		return "", "", errors.New("malformed state payload")
	}
	exp, err := strconv.ParseInt(fields[2], 10, 64)
	if err != nil {
		return "", "", errors.New("malformed state expiry")
	}
	if time.Now().After(time.Unix(exp, 0)) {
		return "", "", errors.New("this connection attempt expired; please try again")
	}
	return fields[0], fields[1], nil
}

func randomNonce() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

// googleAccount reads the connected account's address, so the UI can show which
// Google account is linked rather than an opaque "connected".
func googleAccount(ctx context.Context, client *http.Client) (id, email string) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, userInfoURL, nil)
	if err != nil {
		return "", ""
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", ""
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode >= 400 {
		return "", ""
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 4096))
	if err != nil {
		return "", ""
	}
	var u struct {
		Sub   string `json:"sub"`
		Email string `json:"email"`
	}
	if err := json.Unmarshal(body, &u); err != nil {
		return "", ""
	}
	return u.Sub, u.Email
}
