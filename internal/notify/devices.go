package notify

import (
	"context"
	"net/http"
	"strings"

	"github.com/Autonex009/autonex-crm-api/pkg/httpx"
	"github.com/Autonex009/autonex-crm-api/pkg/middleware"
)

// Device registration for the mobile app.
//
// The app asks the OS for a push token on every launch and posts it here. That
// is deliberately idempotent: Expo can reissue a token after an app update or a
// restore onto a new handset, and the client has no way to know which case it is
// in, so it always re-registers and the server reconciles.

// DeviceInput is what the app sends to register itself.
type DeviceInput struct {
	// Token is an Expo push token, "ExponentPushToken[...]".
	Token string `json:"token"`
	// Platform is "ios" or "android"; anything else is stored as "unknown".
	Platform string `json:"platform"`
	// DeviceName is for the settings screen, so someone can tell which of their
	// handsets a registration belongs to.
	DeviceName string `json:"deviceName"`
}

// SaveDeviceToken records (or re-points) one device registration.
//
// ON CONFLICT on the token moves the row to the current user rather than
// erroring. That is the shared-handset case: the token belongs to the install,
// not the person, so when a colleague signs in on the same phone the previous
// owner must stop receiving that device's pushes.
func (s *Store) SaveDeviceToken(ctx context.Context, userID, orgID string, in DeviceInput) error {
	platform := in.Platform
	if platform != "ios" && platform != "android" {
		platform = "unknown"
	}

	_, err := s.pool.Exec(ctx, `
		INSERT INTO device_push_tokens (user_id, org_id, token, platform, device_name)
		VALUES ($1, $2, $3, $4, NULLIF($5, ''))
		ON CONFLICT (token) DO UPDATE SET
			user_id      = EXCLUDED.user_id,
			org_id       = EXCLUDED.org_id,
			platform     = EXCLUDED.platform,
			device_name  = EXCLUDED.device_name,
			last_seen_at = NOW()
	`, userID, orgID, strings.TrimSpace(in.Token), platform, strings.TrimSpace(in.DeviceName))
	return err
}

// DeviceTokens lists the push tokens for one user, newest install first.
func (s *Store) DeviceTokens(ctx context.Context, userID string) ([]string, error) {
	rows, err := s.pool.Query(ctx,
		`SELECT token FROM device_push_tokens WHERE user_id = $1 ORDER BY last_seen_at DESC`,
		userID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	out := make([]string, 0, 4)
	for rows.Next() {
		var t string
		if err := rows.Scan(&t); err != nil {
			return nil, err
		}
		out = append(out, t)
	}
	return out, rows.Err()
}

// DeleteDeviceToken removes one registration, whoever it belongs to.
//
// Not scoped by user on purpose: this is also how a token Expo reported as
// DeviceNotRegistered gets reaped, and at that point the row may well be
// attached to someone who no longer has the app.
func (s *Store) DeleteDeviceToken(ctx context.Context, token string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM device_push_tokens WHERE token = $1`, token)
	return err
}

// UnreadCount is the number the app shows on its icon badge.
func (s *Store) UnreadCount(ctx context.Context, orgID, userID string) (int, error) {
	var n int
	err := s.pool.QueryRow(ctx,
		`SELECT COUNT(*) FROM notifications
		  WHERE org_id = $1 AND user_id = $2 AND is_read = false`,
		orgID, userID).Scan(&n)
	return n, err
}

// --- HTTP ---

func (h *Handler) registerDevice(w http.ResponseWriter, r *http.Request) {
	var in DeviceInput
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	in.Token = strings.TrimSpace(in.Token)
	if !ValidExpoToken(in.Token) {
		httpx.WriteError(w, http.StatusBadRequest,
			"that is not an Expo push token")
		return
	}

	if err := h.store.SaveDeviceToken(r.Context(),
		middleware.UserID(r.Context()), middleware.OrgID(r.Context()), in); err != nil {
		httpx.WriteServerError(w, "could not register this device", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (h *Handler) unregisterDevice(w http.ResponseWriter, r *http.Request) {
	var in DeviceInput
	if !httpx.DecodeJSON(w, r, &in) {
		return
	}
	if err := h.store.DeleteDeviceToken(r.Context(), strings.TrimSpace(in.Token)); err != nil {
		httpx.WriteServerError(w, "could not unregister this device", err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}
