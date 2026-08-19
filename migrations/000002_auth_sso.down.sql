DROP INDEX IF EXISTS users_provider_idx;
ALTER TABLE users DROP COLUMN IF EXISTS provider_user_id;
ALTER TABLE users DROP COLUMN IF EXISTS auth_provider;
-- Restoring NOT NULL will fail if any SSO (password-less) rows exist; clean those first.
ALTER TABLE users ALTER COLUMN password_hash SET NOT NULL;
