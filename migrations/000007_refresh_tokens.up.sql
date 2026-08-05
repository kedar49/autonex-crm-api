-- Long-lived sessions without long-lived bearer tokens.
--
-- Before this, the only credential was a 15-minute access token, so anyone
-- actually working in the app got dropped at the login screen mid-task. Raising
-- the access TTL would have "fixed" it by making a stolen token useful for a day
-- with no way to revoke it. Refresh tokens keep the access token short-lived and
-- make a session revocable.

CREATE TABLE refresh_tokens (
    id         UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    user_id    UUID NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    -- Only the SHA-256 of the token is stored. The token is 32 bytes of CSPRNG
    -- output, so there is no low entropy for a slow KDF to compensate for; a
    -- leaked dump must not hand out sessions.
    token_hash TEXT NOT NULL UNIQUE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    revoked_at TIMESTAMPTZ,
    -- Rotation chain: every refresh revokes the presented token and points it at
    -- its successor. Presenting an already-revoked token therefore means either a
    -- replay or a stolen copy — see internal/auth/refresh.go.
    replaced_by UUID REFERENCES refresh_tokens(id) ON DELETE SET NULL
);

-- Revoking every session for one user (the reuse-detection response, and the
-- basis of a future "sign out everywhere").
CREATE INDEX refresh_tokens_user_idx ON refresh_tokens (user_id);

-- Supports pruning expired rows without scanning revoked ones.
CREATE INDEX refresh_tokens_expiry_idx ON refresh_tokens (expires_at) WHERE revoked_at IS NULL;
