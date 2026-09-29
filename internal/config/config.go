// Package config loads the env-driven configuration; no globals.
package config

import (
	"os"
	"strconv"
	"time"
)

type Config struct {
	Port          string
	DBPath        string
	MigrationsDir string
	APIBaseURL    string
	APIDelay      time.Duration
	SyncInterval  time.Duration
}

func Load() Config {
	return Config{
		Port:          env("PORT", "8080"),
		DBPath:        env("DB_PATH", "data/congress.db"),
		MigrationsDir: env("MIGRATIONS_DIR", "db/migrations"),
		APIBaseURL:    os.Getenv("API_BASE_URL"), // empty = verified default in camara client
		APIDelay:      durationEnv("API_DELAY_MS", 150*time.Millisecond),
		SyncInterval:  durationEnv("SYNC_INTERVAL", 6*time.Hour),
	}
}

func env(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func durationEnv(k string, def time.Duration) time.Duration {
	if ms, err := strconv.Atoi(os.Getenv(k)); err == nil && ms >= 0 {
		return time.Duration(ms) * time.Millisecond
	}
	return def
}
