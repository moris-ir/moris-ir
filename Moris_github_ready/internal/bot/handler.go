package bot

import (
	"context"
	"fmt"
	"log"
	"moris/connection"
	"moris/internal/queue"
	"moris/internal/ratelimit"
	"moris/internal/storage"
	"moris/lib"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Handler struct {
	Telegram                   *connection.Client
	Store                      *storage.Store
	Queue                      *queue.Queue
	DownloadDir, PublicBaseURL string
	MaxSize                    int64
	LinkTTL                    time.Duration
	Limiter                    *ratelimit.Limiter
	Workers                    int
}

func (h *Handler) Enqueue(ctx context.Context, m *connection.Message) error {
	return h.Queue.Push(ctx, m)
}
func (h *Handler) StartWorkers(ctx context.Context) {
	for i := 0; i < h.Workers; i++ {
		go func(worker int) {
			for {
				m, e := h.Queue.Pop(ctx)
				if e != nil {
					if ctx.Err() == nil {
						log.Printf("queue worker %d: %v", worker, e)
					}
					return
				}
				h.process(ctx, m)
			}
		}(i + 1)
	}
}

func (h *Handler) RunPolling(ctx context.Context) {
	h.StartWorkers(ctx)
	var offset int64
	for {
		updates, e := h.Telegram.GetUpdates(ctx, offset)
		if e != nil {
			if ctx.Err() != nil {
				break
			}
			log.Printf("polling: %v", e)
			time.Sleep(2 * time.Second)
			continue
		}
		for _, u := range updates {
			if u.UpdateID >= offset {
				offset = u.UpdateID + 1
			}
			if u.Message == nil {
				continue
			}
			if e := h.Enqueue(ctx, u.Message); e != nil {
				log.Printf("enqueue: %v", e)
				_ = h.Telegram.SendMessage(ctx, u.Message.Chat.ID, "⏳ صف پردازش موقتاً در دسترس نیست.")
			}
		}
	}
}
func (h *Handler) process(ctx context.Context, m *connection.Message) {
	uid := userID(m)
	_ = h.Store.UpsertUser(ctx, storage.UserRecord{UserID: uid, Username: username(m), FirstName: firstName(m), LastSeen: time.Now().UTC()})
	if m.Text != "" {
		switch strings.TrimSpace(m.Text) {
		case "/start":
			_ = h.Telegram.SendMessage(ctx, m.Chat.ID, "⚡ MORIS\n\nفایل را بفرست تا ذخیره کنم و لینک مستقیم تحویلت بدهم.")
		case "/help":
			_ = h.Telegram.SendMessage(ctx, m.Chat.ID, "📚 راهنما\n\nهر فایل، عکس، ویدئو یا صدا را ارسال کن.\n/start — شروع\n/help — راهنما\n/stats — آمار")
		case "/stats":
			st, _ := h.Store.Stats(ctx)
			_ = h.Telegram.SendMessage(ctx, m.Chat.ID, fmt.Sprintf("📊 MORIS\n\nفایل‌ها: %d\nحجم: %s\nکاربران: %d", st.Files, formatBytes(st.Bytes), st.Users))
		default:
			_ = h.Telegram.SendMessage(ctx, m.Chat.ID, "📎 فایل را برای من ارسال کن.")
		}
		return
	}
	info, ok := extract(m)
	if !ok {
		_ = h.Telegram.SendMessage(ctx, m.Chat.ID, "📎 این نوع پیام فایل قابل ذخیره‌سازی نیست.")
		return
	}
	if !h.Limiter.Allow(uid) {
		_ = h.Telegram.SendMessage(ctx, m.Chat.ID, "🚦 تعداد آپلودها در یک دقیقه زیاد است.")
		return
	}
	if info.Size > 0 && info.Size > h.MaxSize {
		_ = h.Telegram.SendMessage(ctx, m.Chat.ID, fmt.Sprintf("⚠️ حجم فایل از سقف سرویس بیشتر است.\nسقف: %s", formatBytes(h.MaxSize)))
		return
	}
	_ = h.Telegram.SendMessage(ctx, m.Chat.ID, "⏳ فایل در صف پردازش است...")
	f, e := h.Telegram.GetFile(ctx, info.FileID)
	if e != nil {
		log.Printf("getFile: %v", e)
		_ = h.Telegram.SendMessage(ctx, m.Chat.ID, "❌ دریافت فایل ناموفق بود. اگر فایل بزرگ است Local Bot API Server را بررسی کن.")
		return
	}
	token, e := lib.NewToken()
	if e != nil {
		return
	}
	name := lib.SafeFilename(info.Name)
	disk := token + "_" + name
	final := filepath.Join(h.DownloadDir, disk)
	tmp := final + ".part"
	_ = os.MkdirAll(h.DownloadDir, 0755)
	out, e := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if e != nil {
		_ = h.Telegram.SendMessage(ctx, m.Chat.ID, "❌ ایجاد فایل ناموفق بود.")
		return
	}
	e = h.Telegram.DownloadFile(ctx, f.FilePath, out)
	ce := out.Close()
	if e != nil || ce != nil {
		_ = os.Remove(tmp)
		log.Printf("download: %v", e)
		_ = h.Telegram.SendMessage(ctx, m.Chat.ID, "❌ دانلود کامل نشد. برای فایل‌های بزرگ Local Bot API Server لازم است.")
		return
	}
	if e = os.Rename(tmp, final); e != nil {
		_ = os.Remove(tmp)
		return
	}
	st, e := os.Stat(final)
	if e != nil {
		return
	}
	r := storage.FileRecord{Token: token, OriginalName: name, DiskName: disk, MimeType: info.Mime, Size: st.Size(), ChatID: m.Chat.ID, UserID: uid, MessageID: m.MessageID, CreatedAt: time.Now().UTC(), Active: true}
	if h.LinkTTL > 0 {
		r.ExpiresAt = r.CreatedAt.Add(h.LinkTTL)
	}
	if e = h.Store.Add(ctx, r); e != nil {
		_ = os.Remove(final)
		_ = h.Telegram.SendMessage(ctx, m.Chat.ID, "❌ ثبت اطلاعات فایل ناموفق بود.")
		return
	}
	direct := h.PublicBaseURL + "/d/" + token
	page := h.PublicBaseURL + "/v/" + token
	_ = h.Telegram.SendMessage(ctx, m.Chat.ID, fmt.Sprintf("✅ آپلود شد!\n\n📄 %s\n📦 %s\n\n🔗 %s\n🌐 %s", name, formatBytes(st.Size()), direct, page))
}

type mediaInfo struct {
	ID, Name, Mime string
	Size           int64
}

func extract(m *connection.Message) (mediaInfo, bool) {
	if m.Document != nil {
		return mediaInfo{m.Document.FileID, m.Document.FileName, m.Document.MimeType, m.Document.FileSize}, true
	}
	if m.Video != nil {
		return mediaInfo{m.Video.FileID, or(m.Video.FileName, "video.mp4"), m.Video.MimeType, m.Video.FileSize}, true
	}
	if m.Audio != nil {
		return mediaInfo{m.Audio.FileID, or(m.Audio.FileName, "audio"), m.Audio.MimeType, m.Audio.FileSize}, true
	}
	if m.Animation != nil {
		return mediaInfo{m.Animation.FileID, or(m.Animation.FileName, "animation"), m.Animation.MimeType, m.Animation.FileSize}, true
	}
	if m.Voice != nil {
		return mediaInfo{m.Voice.FileID, "voice.ogg", m.Voice.MimeType, m.Voice.FileSize}, true
	}
	if len(m.Photo) > 0 {
		p := m.Photo[len(m.Photo)-1]
		return mediaInfo{p.FileID, "photo.jpg", "image/jpeg", p.FileSize}, true
	}
	return mediaInfo{}, false
}
func or(a, b string) string {
	if a == "" {
		return b
	}
	return a
}
func userID(m *connection.Message) int64 {
	if m.From != nil {
		return m.From.ID
	}
	return m.Chat.ID
}
func username(m *connection.Message) string {
	if m.From != nil {
		return m.From.Username
	}
	return ""
}
func firstName(m *connection.Message) string {
	if m.From != nil {
		return m.From.FirstName
	}
	return ""
}
func formatBytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	v := float64(n)
	for _, u := range []string{"KB", "MB", "GB", "TB"} {
		v /= 1024
		if v < 1024 {
			return fmt.Sprintf("%.2f %s", v, u)
		}
	}
	return fmt.Sprintf("%.2f PB", v)
}
