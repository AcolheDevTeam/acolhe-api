package config

import "os"

type Config struct {
	Port           string
	DatabaseURL    string
	JWTSecret      string
	RedisAddr      string
	S3Endpoint     string
	S3Region       string
	S3Bucket       string
	S3AccessKeyID  string
	S3SecretKey    string
	S3SessionToken string
	SMTPAddress    string
	SMTPUsername   string
	SMTPPassword   string
	SMTPFrom       string
}

func Load() Config {
	return Config{
		Port:           env("PORT", "8080"),
		DatabaseURL:    env("DATABASE_URL", "postgres://localhost:5432/acolhe?sslmode=disable"),
		JWTSecret:      env("JWT_SECRET", "dev-secret-troca-em-producao"),
		RedisAddr:      env("REDIS_ADDR", "127.0.0.1:6379"),
		S3Endpoint:     os.Getenv("S3_ENDPOINT"),
		S3Region:       os.Getenv("S3_REGION"),
		S3Bucket:       os.Getenv("S3_BUCKET"),
		S3AccessKeyID:  os.Getenv("S3_ACCESS_KEY_ID"),
		S3SecretKey:    os.Getenv("S3_SECRET_ACCESS_KEY"),
		S3SessionToken: os.Getenv("S3_SESSION_TOKEN"),
		SMTPAddress:    os.Getenv("SMTP_ADDRESS"),
		SMTPUsername:   os.Getenv("SMTP_USERNAME"),
		SMTPPassword:   os.Getenv("SMTP_PASSWORD"),
		SMTPFrom:       os.Getenv("SMTP_FROM"),
	}
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
