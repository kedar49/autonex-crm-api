// Package integrations owns the third-party accounts a user connects to the
// CRM, and the OAuth dance that establishes them.
//
// The sqlc package under db/ targets an older shape of integration_connections
// (org_id, encrypted_tokens) that this deployment's table does not have; this
// hand-written store speaks to the columns that actually exist, the same way the
// auth module does.
package integrations

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"golang.org/x/oauth2"
)

// ErrNotConnected means the user has no stored token for that provider.
var ErrNotConnected = errors.New("provider is not connected")

// Connection is one user's link to a third-party account.
type Connection struct {
	Provider          string     `json:"provider"`
	ProviderAccountID string     `json:"providerAccountId"`
	Scope             string     `json:"scope"`
	ExpiresAt         *time.Time `json:"expiresAt"`
	ConnectedAt       time.Time  `json:"connectedAt"`
}

type store struct {
	pool *pgxpool.Pool
}

// list returns what the caller has connected, without the tokens — the Settings
// screen needs to know that Google is linked, not the secret that links it.
func (s *store) list(ctx context.Context, userID string) ([]Connection, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT provider, COALESCE(provider_account_id, ''), COALESCE(scope, ''),
		        expires_at, created_at
		   FROM integration_connections
		  WHERE user_id = $1
		  ORDER BY provider`, userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]Connection, 0, 2)
	for rows.Next() {
		var c Connection
		if err := rows.Scan(&c.Provider, &c.ProviderAccountID, &c.Scope,
			&c.ExpiresAt, &c.ConnectedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// token reads the stored OAuth token for a provider.
func (s *store) token(ctx context.Context, userID, provider string) (*oauth2.Token, error) {
	var (
		access  string
		refresh *string
		expiry  *time.Time
	)
	err := s.pool.QueryRow(ctx,
		`SELECT access_token, refresh_token, expires_at
		   FROM integration_connections
		  WHERE user_id = $1 AND provider = $2`, userID, provider).
		Scan(&access, &refresh, &expiry)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotConnected
	}
	if err != nil {
		return nil, err
	}

	tok := &oauth2.Token{AccessToken: access, TokenType: "Bearer"}
	if refresh != nil {
		tok.RefreshToken = *refresh
	}
	if expiry != nil {
		tok.Expiry = *expiry
	}
	return tok, nil
}

// save records a freshly granted token, replacing any previous one for that
// user and provider.
//
// Google returns a refresh token only on the first consent, so an empty one on a
// re-authorisation must not wipe the stored value — COALESCE keeps it.
func (s *store) save(ctx context.Context, userID, provider string, tok *oauth2.Token, accountID string) error {
	var expiry *time.Time
	if !tok.Expiry.IsZero() {
		e := tok.Expiry
		expiry = &e
	}
	var refresh *string
	if tok.RefreshToken != "" {
		r := tok.RefreshToken
		refresh = &r
	}

	_, err := s.pool.Exec(ctx,
		`INSERT INTO integration_connections
		   (user_id, provider, access_token, refresh_token, expires_at, scope,
		    provider_account_id)
		 VALUES ($1, $2, $3, $4, $5, $6, NULLIF($7, ''))
		 ON CONFLICT (user_id, provider) DO UPDATE
		 SET access_token        = EXCLUDED.access_token,
		     refresh_token       = COALESCE(EXCLUDED.refresh_token,
		                                    integration_connections.refresh_token),
		     expires_at          = EXCLUDED.expires_at,
		     scope               = EXCLUDED.scope,
		     provider_account_id = COALESCE(EXCLUDED.provider_account_id,
		                                    integration_connections.provider_account_id),
		     updated_at          = now()`,
		userID, provider, tok.AccessToken, refresh, expiry,
		scopeOf(tok), accountID)
	return err
}

// refreshed persists a token the oauth2 library rotated for us mid-request.
func (s *store) refreshed(ctx context.Context, userID, provider string, tok *oauth2.Token) error {
	var expiry *time.Time
	if !tok.Expiry.IsZero() {
		e := tok.Expiry
		expiry = &e
	}
	_, err := s.pool.Exec(ctx,
		`UPDATE integration_connections
		    SET access_token = $3, expires_at = $4, updated_at = now()
		  WHERE user_id = $1 AND provider = $2`,
		userID, provider, tok.AccessToken, expiry)
	return err
}

func (s *store) delete(ctx context.Context, userID, provider string) error {
	_, err := s.pool.Exec(ctx,
		`DELETE FROM integration_connections WHERE user_id = $1 AND provider = $2`,
		userID, provider)
	return err
}

// saveCalendarEvent records the booked meeting against its lead or deal.
//
// google_event_id is unique, so a retry of the same booking updates the row
// rather than duplicating it.
func (s *store) saveCalendarEvent(ctx context.Context, m Meeting, leadID, dealID string) error {
	_, err := s.pool.Exec(ctx,
		`INSERT INTO calendar_events
		   (google_event_id, title, start_at, end_at, meet_link, lead_id, deal_id)
		 VALUES ($1, $2, $3, $4, NULLIF($5, ''), NULLIF($6, '')::uuid, NULLIF($7, '')::uuid)
		 ON CONFLICT (google_event_id) DO UPDATE
		 SET title      = EXCLUDED.title,
		     start_at   = EXCLUDED.start_at,
		     end_at     = EXCLUDED.end_at,
		     meet_link  = EXCLUDED.meet_link,
		     synced_at  = now(),
		     updated_at = now()`,
		m.GoogleEventID, m.Title, m.StartAt, m.EndAt, m.MeetLink, leadID, dealID)
	return err
}

// scopeOf reads the space-separated scope list Google returns alongside the
// token. It is stored so the UI can tell a calendar connection apart from one
// granted for something else later.
func scopeOf(tok *oauth2.Token) string {
	if v, ok := tok.Extra("scope").(string); ok {
		return v
	}
	return ""
}
