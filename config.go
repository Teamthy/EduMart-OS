package config

import "os"

type Config struct { AppEnv string; HTTPAddr string; DatabaseURL string }

func Load() Config {
	return Config{AppEnv: env("APP_ENV", "development"), HTTPAddr: env("HTTP_ADDR", ":8080"), DatabaseURL: env("DATABASE_URL", "")}
}
func env(key, fallback string) string { if value := os.Getenv(key); value != "" { return value }; return fallback }
