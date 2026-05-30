package config

import "os"

type Config struct {
	Port        string
	DatabaseURL string
	JWTSecret   string
	RedisAddr   string
}

func Load() Config {
	return Config{
		Port:        env("PORT", "8080"),
		DatabaseURL: env("DATABASE_URL", "postgres://localhost:5432/acolhe?sslmode=disable"),
		JWTSecret:   env("JWT_SECRET", "dev-secret-troca-em-producao"),
		RedisAddr:   env("REDIS_ADDR", "127.0.0.1:6379"),
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
