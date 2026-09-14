package httpserver

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"html/template"
	"io"
	"log"
	"mime/multipart"
	"moris/connection"
	"moris/internal/storage"
	"moris/lib"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Enqueuer interface {
	Enqueue(context.Context, *connection.Message) error
}

// Server provides the admin UI, public file service, multipart uploader and Telegram webhook.
type Server struct {
	Store                                                                        *storage.Store
	DownloadDir, PublicBaseURL, AdminUser, AdminPass, WebhookPath, WebhookSecret string
	MaxFileSize                                                                  int64
	Template                                                                     *template.Template
	sessions                                                                     map[string]time.Time
	mu                                                                           sync.Mutex
	Bot                                                                          interface {
		Enqueue(context.Context, *connection.Message) error
	}
}

func New(store *storage.Store, dir, base, user, pass, webhookPath, webhookSecret string, maxSize int64, bot interface {
	Enqueue(context.Context, *connection.Message) error
}) (*Server, error) {
	t, err := template.New("root").Funcs(template.FuncMap{"formatBytes": formatBytes}).ParseFiles("web/templates/download.html", "web/templates/admin.html")
	if err != nil {
		return nil, err
	}
	return &Server{Store: store, DownloadDir: dir, PublicBaseURL: strings.TrimRight(base, "/"), AdminUser: user, AdminPass: pass, WebhookPath: webhookPath, WebhookSecret: webhookSecret, MaxFileSize: maxSize, Template: t, sessions: map[string]time.Time{}, Bot: bot}, nil
}
func (s *Server) Handler() http.Handler {
	m := http.NewServeMux()
	m.HandleFunc("/", s.home)
	m.HandleFunc("/healthz", s.health)
	m.HandleFunc("/d/", s.download)
	m.HandleFunc("/v/", s.view)
	m.HandleFunc("/api/file/", s.api)
	m.HandleFunc("/api/upload", s.upload)
	m.HandleFunc(s.WebhookPath, s.webhook)
	m.HandleFunc("/admin", s.admin)
	m.HandleFunc("/admin/login", s.login)
	m.HandleFunc("/admin/logout", s.logout)
	m.HandleFunc("/admin/delete", s.delete)
	m.HandleFunc("/admin/stats", s.stats)
	m.HandleFunc("/admin/users", s.users)
	m.HandleFunc("/admin/settings", s.settings)
	return logMiddleware(m)
}
func (s *Server) home(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	io.WriteString(w, `<!doctype html><html lang="fa" dir="rtl"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>MORIS</title><style>body{font-family:system-ui;background:#080d19;color:#fff;min-height:100vh;display:grid;place-items:center}.card{padding:48px;border-radius:30px;background:#121b2e;border:1px solid #273552;text-align:center;box-shadow:0 25px 90px #0009}h1{font-size:64px;margin:0}p{color:#9eabc7;font-size:18px}.up{margin-top:22px}</style><div class=card><h1>⚡ MORIS</h1><p>Telegram File Uploader</p><p>سرویس آماده است.</p><form class=up action="/api/upload" method="post" enctype="multipart/form-data"><input type="file" name="files" multiple required><button>آپلود چندفایلی</button></form></div>`)
}
func (s *Server) health(w http.ResponseWriter, r *http.Request) {
	st, e := s.Store.Stats(r.Context())
	w.Header().Set("Content-Type", "application/json")
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "files": st.Files, "bytes": st.Bytes, "users": st.Users})
}
func (s *Server) record(ctx context.Context, token string) (storage.FileRecord, bool) {
	r, ok, e := s.Store.Get(ctx, token)
	if e != nil {
		return storage.FileRecord{}, false
	}
	return r, ok
}
func (s *Server) download(w http.ResponseWriter, r *http.Request) {
	s.serve(w, r, strings.TrimPrefix(r.URL.Path, "/d/"), true)
}
func (s *Server) view(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.record(r.Context(), strings.TrimPrefix(r.URL.Path, "/v/"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	s.Template.ExecuteTemplate(w, "download.html", map[string]any{"Name": rec.OriginalName, "Size": formatBytes(rec.Size), "URL": s.PublicBaseURL + "/d/" + rec.Token})
}
func (s *Server) serve(w http.ResponseWriter, r *http.Request, token string, force bool) {
	rec, ok := s.record(r.Context(), token)
	if !ok {
		http.NotFound(w, r)
		return
	}
	path := filepath.Join(s.DownloadDir, rec.DiskName)
	if _, e := os.Stat(path); e != nil {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Accept-Ranges", "bytes")
	if rec.MimeType != "" {
		w.Header().Set("Content-Type", rec.MimeType)
	}
	disp := "inline"
	if force {
		disp = "attachment"
	}
	w.Header().Set("Content-Disposition", disp+`; filename="`+safeHeader(rec.OriginalName)+`"`)
	http.ServeFile(w, r, path)
}
func (s *Server) api(w http.ResponseWriter, r *http.Request) {
	rec, ok := s.record(r.Context(), strings.TrimPrefix(r.URL.Path, "/api/file/"))
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"name": rec.OriginalName, "mime": rec.MimeType, "size": rec.Size, "created_at": rec.CreatedAt, "expires_at": rec.ExpiresAt, "download_url": s.PublicBaseURL + "/d/" + rec.Token})
}
func (s *Server) admin(w http.ResponseWriter, r *http.Request) {
	if !s.auth(w, r) {
		return
	}
	files, _ := s.Store.Search(r.Context(), r.URL.Query().Get("q"), 50, 0)
	st, _ := s.Store.Stats(r.Context())
	s.Template.ExecuteTemplate(w, "admin.html", map[string]any{"Files": files, "Stats": st, "Base": s.PublicBaseURL, "Q": r.URL.Query().Get("q"), "Max": formatBytes(s.MaxFileSize)})
}
func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	if r.Method == "GET" {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		io.WriteString(w, `<!doctype html><html lang="fa" dir="rtl"><meta charset="utf-8"><meta name="viewport" content="width=device-width,initial-scale=1"><title>MORIS Login</title><style>body{font-family:system-ui;background:#080d19;color:#fff;display:grid;place-items:center;min-height:100vh}.box{background:#121b2e;padding:30px;border-radius:22px;width:min(400px,90%)}input,button{width:100%;padding:13px;margin:8px 0;border-radius:10px;border:1px solid #33415f;background:#0c1322;color:white}button{cursor:pointer}</style><form class=box method=post><h2>⚡ MORIS Admin</h2><input name=username placeholder="نام کاربری" autofocus><input name=password type=password placeholder="رمز عبور"><button>ورود</button></form>`)
		return
	}
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}
	if r.FormValue("username") != s.AdminUser || r.FormValue("password") != s.AdminPass {
		http.Error(w, "نام کاربری یا رمز عبور نادرست است", 401)
		return
	}
	b := make([]byte, 32)
	if _, e := rand.Read(b); e != nil {
		http.Error(w, "session error", 500)
		return
	}
	token := hex.EncodeToString(b)
	s.mu.Lock()
	s.sessions[token] = time.Now().Add(24 * time.Hour)
	s.mu.Unlock()
	http.SetCookie(w, &http.Cookie{Name: "moris_session", Value: token, Path: "/", HttpOnly: true, SameSite: http.SameSiteLaxMode, MaxAge: 86400})
	http.Redirect(w, r, "/admin", 303)
}
func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{Name: "moris_session", Value: "", Path: "/", MaxAge: -1, HttpOnly: true})
	http.Redirect(w, r, "/admin/login", 303)
}
func (s *Server) auth(w http.ResponseWriter, r *http.Request) bool {
	c, e := r.Cookie("moris_session")
	if e == nil {
		s.mu.Lock()
		exp, ok := s.sessions[c.Value]
		if ok && time.Now().Before(exp) {
			s.mu.Unlock()
			return true
		}
		delete(s.sessions, c.Value)
		s.mu.Unlock()
	}
	http.Redirect(w, r, "/admin/login", 303)
	return false
}
func (s *Server) delete(w http.ResponseWriter, r *http.Request) {
	if !s.auth(w, r) {
		return
	}
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}
	rec, ok, e := s.Store.Delete(r.Context(), r.FormValue("token"))
	if e != nil {
		http.Error(w, "storage error", 500)
		return
	}
	if !ok {
		http.NotFound(w, r)
		return
	}
	_ = os.Remove(filepath.Join(s.DownloadDir, rec.DiskName))
	http.Redirect(w, r, "/admin", 303)
}
func (s *Server) stats(w http.ResponseWriter, r *http.Request) {
	if !s.auth(w, r) {
		return
	}
	st, e := s.Store.Stats(r.Context())
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(st)
}
func (s *Server) users(w http.ResponseWriter, r *http.Request) {
	if !s.auth(w, r) {
		return
	}
	if r.URL.Query().Get("json") == "1" {
		u, e := s.Store.Users(r.Context(), 100, 0)
		if e != nil {
			http.Error(w, e.Error(), 500)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(u)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	t, e := template.ParseFiles("web/templates/users.html")
	if e != nil {
		http.Error(w, e.Error(), 500)
		return
	}
	t.ExecuteTemplate(w, "users.html", nil)
}
func (s *Server) settings(w http.ResponseWriter, r *http.Request) {
	if !s.auth(w, r) {
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	fmt.Fprintf(w, `<!doctype html><html lang="fa" dir="rtl"><meta charset="utf-8"><title>MORIS Settings</title><body style="font-family:system-ui;background:#080d19;color:white;padding:30px"><h1>⚙️ تنظیمات</h1><p>سقف فعلی: %s</p><p>Telegram API: %s</p><p>Webhook: %s</p><p>تنظیمات عملیاتی از طریق فایل <code>.env</code> کنترل می‌شوند؛ پس از تغییر، سرویس را restart کنید.</p><a href="/admin">بازگشت</a></body></html>`, formatBytes(s.MaxFileSize), "configured", s.WebhookPath)
}
func (s *Server) upload(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}
	if r.ContentLength > s.MaxFileSize*10 {
		http.Error(w, "payload too large", 413)
		return
	}
	if e := r.ParseMultipartForm(64 << 20); e != nil {
		http.Error(w, e.Error(), 400)
		return
	}
	type result struct {
		Name, URL string
		Size      int64
	}
	out := []result{}
	for _, fh := range r.MultipartForm.File["files"] {
		if fh.Size > s.MaxFileSize {
			http.Error(w, "file exceeds configured limit", 413)
			return
		}
		f, e := fh.Open()
		if e != nil {
			http.Error(w, e.Error(), 400)
			return
		}
		x, e := s.saveUploaded(r, fh, f)
		f.Close()
		if e != nil {
			http.Error(w, e.Error(), 500)
			return
		}
		out = append(out, result{x.OriginalName, s.PublicBaseURL + "/d/" + x.Token, x.Size})
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"ok": true, "files": out})
}
func (s *Server) saveUploaded(r *http.Request, fh *multipart.FileHeader, f multipart.File) (storage.FileRecord, error) {
	token, e := lib.NewToken()
	if e != nil {
		return storage.FileRecord{}, e
	}
	name := lib.SafeFilename(fh.Filename)
	disk := token + "_" + name
	_ = os.MkdirAll(s.DownloadDir, 0755)
	tmp := filepath.Join(s.DownloadDir, disk+".part")
	out, e := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0644)
	if e != nil {
		return storage.FileRecord{}, e
	}
	n, e := io.Copy(out, io.LimitReader(f, s.MaxFileSize+1))
	ce := out.Close()
	if e != nil || ce != nil {
		os.Remove(tmp)
		return storage.FileRecord{}, fmt.Errorf("write upload: %v", e)
	}
	if n > s.MaxFileSize {
		os.Remove(tmp)
		return storage.FileRecord{}, fmt.Errorf("file too large")
	}
	final := filepath.Join(s.DownloadDir, disk)
	if e = os.Rename(tmp, final); e != nil {
		return storage.FileRecord{}, e
	}
	mime := fh.Header.Get("Content-Type")
	r0 := storage.FileRecord{Token: token, OriginalName: name, DiskName: disk, MimeType: mime, Size: n, CreatedAt: time.Now().UTC(), Active: true}
	if e = s.Store.Add(r.Context(), r0); e != nil {
		os.Remove(final)
		return storage.FileRecord{}, e
	}
	return r0, nil
}
func (s *Server) webhook(w http.ResponseWriter, r *http.Request) {
	if r.Method != "POST" {
		http.Error(w, "method not allowed", 405)
		return
	}
	if s.WebhookSecret != "" && r.Header.Get("X-Telegram-Bot-Api-Secret-Token") != s.WebhookSecret {
		http.Error(w, "forbidden", 403)
		return
	}
	var u connection.Update
	if e := json.NewDecoder(http.MaxBytesReader(w, r.Body, 1<<20)).Decode(&u); e != nil {
		http.Error(w, "bad update", 400)
		return
	}
	if u.Message != nil {
		if e := s.Bot.Enqueue(r.Context(), u.Message); e != nil {
			http.Error(w, "queue error", 503)
			return
		}
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte(`{"ok":true}`))
}
func safeHeader(v string) string {
	return strings.NewReplacer(`"`, "", `\`, "", "\r", "", "\n", "").Replace(v)
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
func logMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		log.Printf("%s %s", r.Method, r.URL.Path)
		next.ServeHTTP(w, r)
	})
}
