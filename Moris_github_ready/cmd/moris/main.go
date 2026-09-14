package main

import (
	"context"
	"log"
	"moris/config"
	"moris/connection"
	"moris/internal/bot"
	"moris/internal/httpserver"
	"moris/internal/queue"
	"moris/internal/ratelimit"
	"moris/internal/storage"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"
)

func main() {
	cfg := config.Load()
	for _, d := range []string{cfg.DownloadDir, cfg.DataDir, cfg.LogDir} {
		if e := os.MkdirAll(d, 0755); e != nil {
			log.Fatalf("mkdir %s: %v", d, e)
		}
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	store, e := storage.Open(ctx, cfg.PostgresURL)
	if e != nil {
		log.Fatalf("postgres: %v", e)
	}
	defer store.Close()
	q, e := queue.New(ctx, cfg.RedisURL, cfg.RedisQueue)
	if e != nil {
		log.Fatalf("redis: %v", e)
	}
	defer q.Close()
	tg := connection.New(cfg.TelegramToken, cfg.TelegramAPIBase)
	h := &bot.Handler{Telegram: tg, Store: store, Queue: q, DownloadDir: cfg.DownloadDir, PublicBaseURL: cfg.PublicBaseURL, MaxSize: cfg.MaxFileSizeBytes, Workers: cfg.Workers, Limiter: ratelimit.New(cfg.RateLimitPerMinute), LinkTTL: time.Duration(cfg.LinkTTLHours) * time.Hour}
	web, e := httpserver.New(store, cfg.DownloadDir, cfg.PublicBaseURL, cfg.AdminUsername, cfg.AdminPassword, cfg.WebhookPath, cfg.WebhookSecret, cfg.MaxFileSizeBytes, h)
	if e != nil {
		log.Fatalf("web: %v", e)
	}
	srv := &http.Server{Addr: cfg.HTTPAddr, Handler: web.Handler(), ReadHeaderTimeout: 10 * time.Second, ReadTimeout: 30 * time.Minute, WriteTimeout: 30 * time.Minute, IdleTimeout: 2 * time.Minute, MaxHeaderBytes: 1 << 20}
	go func() {
		log.Printf("MORIS listening on %s", cfg.HTTPAddr)
		if e := srv.ListenAndServe(); e != nil && e != http.ErrServerClosed {
			log.Fatalf("http: %v", e)
		}
	}()
	go cleanup(ctx, store, cfg.DownloadDir, time.Duration(cfg.CleanupIntervalMinutes)*time.Minute)
	h.StartWorkers(ctx)
	if cfg.BotMode == "polling" {
		go h.RunPolling(ctx)
	}
	<-ctx.Done()
	shutdown, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = srv.Shutdown(shutdown)
	log.Println("MORIS stopped")
}
func cleanup(ctx context.Context, s *storage.Store, dir string, interval time.Duration) {
	if interval < time.Minute {
		interval = time.Minute
	}
	run := func() {
		rows, e := s.DeleteExpired(ctx)
		if e != nil {
			log.Printf("cleanup: %v", e)
			return
		}
		for _, r := range rows {
			_ = os.Remove(filepath.Join(dir, r.DiskName))
		}
		if len(rows) > 0 {
			log.Printf("cleanup: removed %d expired files", len(rows))
		}
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	run()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			run()
		}
	}
}
