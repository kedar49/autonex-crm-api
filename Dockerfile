# syntax=docker/dockerfile:1.7
#
# Go gateway (cmd/gateway) for Railway.
#
# The build context is the repo root, which is also the Go module root, so the
# image carries migrations/ for the pre-deploy migration step alongside the
# binary.

# ---------- build ----------
FROM golang:1.25-alpine AS build
WORKDIR /src

# Module download is its own layer: it only re-runs when go.mod/go.sum change,
# which is rarely.
COPY go.mod go.sum ./
RUN go mod download

COPY . ./

# CGO off => a static binary that runs on a bare alpine with no libc surprises.
# -trimpath drops build paths, -s -w drops the symbol table and DWARF: a
# noticeably smaller binary, so a faster image pull and cold start.
RUN CGO_ENABLED=0 GOOS=linux go build \
      -trimpath -ldflags="-s -w" \
      -o /out/gateway ./cmd/gateway

# golang-migrate, postgres driver only, built statically so the release image can
# run migrations itself without shipping a second toolchain.
RUN CGO_ENABLED=0 go install -tags 'postgres' \
      github.com/golang-migrate/migrate/v4/cmd/migrate@v4.17.1

# ---------- runtime ----------
FROM alpine:3.20 AS runtime

# ca-certificates: Supabase TLS and the OIDC providers.
# tzdata: APP_TIMEZONE is resolved with time.LoadLocation at boot (see
#   pkg/database/pool.go) — without the zone database the gateway refuses to
#   start on anything but UTC.
RUN apk add --no-cache ca-certificates tzdata \
    && adduser -D -u 10001 app

WORKDIR /app
COPY --from=build /out/gateway    /app/gateway
COPY --from=build /go/bin/migrate /usr/local/bin/migrate
COPY migrations                   /app/migrations
COPY scripts/predeploy.sh         /app/predeploy.sh
# The exec bit does not survive a checkout on Windows, so set it here.
RUN chmod +x /app/predeploy.sh

USER app
EXPOSE 8080
CMD ["/app/gateway"]
