#!/bin/sh
# Pre-deploy migration step, run by Railway before the new container takes traffic.
#
# Every deploy failure this project has had came from DATABASE_URL rather than
# from the SQL, and the raw driver errors do not say which part of the URL is
# wrong. So: echo the target with the password stripped, name the two mistakes
# that are invisible in the error text, then hand off to migrate.
set -e

if [ -z "$DATABASE_URL" ]; then
    echo "pre-deploy: DATABASE_URL is not set on this service" >&2
    exit 1
fi

# Strip the password only — user, host, port and params are exactly what a
# failure diagnosis needs, and none of them are secret.
echo "pre-deploy: target $(printf '%s' "$DATABASE_URL" | sed -E 's#(://[^:]*:)[^@]*@#\1***@#')"

case "$DATABASE_URL" in
    *db.*.supabase.co*)
        echo "pre-deploy: WARNING direct Supabase host is IPv6-only and Railway has no IPv6 egress." >&2
        echo "pre-deploy:         expect 'network is unreachable'. Use the session pooler host." >&2
        ;;
esac
case "$DATABASE_URL" in
    *:6543/*)
        echo "pre-deploy: WARNING port 6543 is the transaction-mode pooler." >&2
        echo "pre-deploy:         golang-migrate needs a session-level advisory lock. Use port 5432." >&2
        ;;
esac
case "$DATABASE_URL" in
    *sslmode=*) ;;
    *) echo "pre-deploy: WARNING no sslmode set; pgx may silently fall back to an unencrypted connection." >&2 ;;
esac

exec migrate -verbose -path /app/migrations -database "$DATABASE_URL" up
