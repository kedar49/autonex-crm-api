-- SSO support: users may authenticate via an external OIDC provider instead of a password.
ALTER TABLE users ALTER COLUMN password_hash DROP NOT NULL;
ALTER TABLE users ADD COLUMN auth_provider     TEXT NOT NULL DEFAULT 'password';
ALTER TABLE users ADD COLUMN provider_user_id  TEXT;

-- One account per (provider, external id). Partial index so multiple password
-- users (provider_user_id IS NULL) never collide.
CREATE UNIQUE INDEX users_provider_idx
    ON users (auth_provider, provider_user_id)
    WHERE provider_user_id IS NOT NULL;
