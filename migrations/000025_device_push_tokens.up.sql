-- Expo push tokens, one row per installed app instance.
--
-- Separate from push_subscriptions rather than folded into it: that table holds
-- W3C Web Push subscriptions (endpoint + p256dh + auth), which are a browser
-- concept with an encryption keypair. A native client has none of that — it has
-- a single opaque "ExponentPushToken[...]" string that Expo's service routes to
-- APNs or FCM. Forcing both into one table would mean three nullable columns and
-- a kind discriminator on every read.
CREATE TABLE IF NOT EXISTS public.device_push_tokens (
  id           UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id      UUID NOT NULL REFERENCES public.users(id) ON DELETE CASCADE,
  org_id       VARCHAR(64) NOT NULL,
  -- UNIQUE because the token identifies the installation, not the person: when
  -- a second user signs in on the same handset the row must move to them, or
  -- the previous owner keeps receiving that device's notifications.
  token        TEXT NOT NULL UNIQUE,
  platform     VARCHAR(16) NOT NULL DEFAULT 'unknown',
  device_name  TEXT,
  created_at   TIMESTAMPTZ NOT NULL DEFAULT NOW(),
  -- Refreshed on every app launch. Expo tokens do not expire on a schedule, so
  -- this is the only signal that an install is gone and the row can be reaped.
  last_seen_at TIMESTAMPTZ NOT NULL DEFAULT NOW()
);

CREATE INDEX IF NOT EXISTS idx_device_push_tokens_user ON public.device_push_tokens(user_id);
