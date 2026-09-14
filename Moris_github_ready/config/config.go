package config

import (
	"log"
	"os"
	"strconv"
	"strings"
)

type Config struct {
	AppName, TelegramToken, TelegramAPIBase, PublicBaseURL, HTTPAddr string
	DownloadDir, DataDir, LogDir                                     string
	MaxFileSizeBytes                                                 int64
	Workers, RateLimitPerMinute, LinkTTLHours                        int
	AdminUsername, AdminPassword                                     string
	PostgresURL, RedisURL, RedisQueue                                string
	SessionTTLHours, CleanupIntervalMinutes                          int
	WebhookPath, WebhookSecret, BotMode                              string
}

func Load() Config {
	maxMB := envInt("MAX_FILE_SIZE_MB", 2048)
	if maxMB < 0 {
		maxMB = 0
	}
	workers := envInt("WORKERS", 8)
	if workers < 1 {
		workers = 1
	}
	rate := envInt("RATE_LIMIT_PER_MINUTE", 30)
	if rate < 1 {
		rate = 1
	}
	ttl := envInt("LINK_TTL_HOURS", 0)
	if ttl < 0 {
		ttl = 0
	}
	mode := strings.ToLower(env("BOT_MODE", "webhook"))
	if mode != "polling" && mode != "webhook" {
		mode = "webhook"
	}
	c := Config{
		AppName: env("APP_NAME", "MORIS"), TelegramToken: strings.TrimSpace(os.Getenv("TELEGRAM_BOT_TOKEN")),
		TelegramAPIBase: strings.TrimRight(env("TELEGRAM_API_BASE", "http://telegram-bot-api:8081"), "/"),
		PublicBaseURL:   strings.TrimRight(env("PUBLIC_BASE_URL", "http://localhost:8080"), "/"), HTTPAddr: env("HTTP_ADDR", ":8080"),
		DownloadDir: env("DOWNLOAD_DIR", "./downloads"), DataDir: env("DATA_DIR", "./data"), LogDir: env("LOG_DIR", "./logs"),
		MaxFileSizeBytes: int64(maxMB) * 1024 * 1024, Workers: workers, RateLimitPerMinute: rate, LinkTTLHours: ttl,
		AdminUsername: os.Getenv("ADMIN_USERNAME"), AdminPassword: os.Getenv("ADMIN_PASSWORD"),
		PostgresURL: env("POSTGRES_URL", "postgres://moris:moris@postgres:5432/moris?sslmode=disable"),
		RedisURL:    env("REDIS_URL", "redis://redis:6379/0"), RedisQueue: env("REDIS_QUEUE", "moris:uploads"),
		SessionTTLHours: envInt("SESSION_TTL_HOURS", 24), CleanupIntervalMinutes: envInt("CLEANUP_INTERVAL_MINUTES", 15),
		WebhookPath: env("WEBHOOK_PATH", "/telegram/webhook"), WebhookSecret: os.Getenv("WEBHOOK_SECRET"), BotMode: mode,
	}
	if c.TelegramToken == "" {
		log.Fatal("TELEGRAM_BOT_TOKEN is required")
	}
	if c.AdminUsername == "" || c.AdminPassword == "" {
		log.Fatal("ADMIN_USERNAME and ADMIN_PASSWORD are required")
	}
	if c.MaxFileSizeBytes == 0 {
		c.MaxFileSizeBytes = 2 * 1024 * 1024 * 1024
	}
	return c
}
func env(k, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return fallback
}
func envInt(k string, fallback int) int {
	v, e := strconv.Atoi(strings.TrimSpace(os.Getenv(k)))
	if e != nil {
		return fallback
	}
	return v
}
