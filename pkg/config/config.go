package config

import "os"

// Config holds runtime configuration loaded from the environment.
type Config struct {
	DatabaseURL string
	JWTSecret   string
	JWTIssuer   string
	NATSURL     string
	GatewayAddr string
}

// Load reads configuration from environment variables with sane defaults.
func Load() Config {
	return Config{
		DatabaseURL: getenv("DATABASE_URL", "postgres://postgres:postgres@localhost:5432/gocrm?sslmode=disable"),
		JWTSecret:   getenv("JWT_SECRET", "change-me-in-production"),
		JWTIssuer:   getenv("JWT_ISSUER", "go-crm"),
		NATSURL:     getenv("NATS_URL", "nats://localhost:4222"),
		GatewayAddr: getenv("GATEWAY_ADDR", ":8080"),
	}
}

func getenv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
