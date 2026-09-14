package storage

import (
	"context"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"time"
)

type FileRecord struct {
	Token, OriginalName, DiskName, MimeType string
	Size, ChatID, UserID, MessageID         int64
	CreatedAt, ExpiresAt                    time.Time
	Active                                  bool
}
type UserRecord struct {
	UserID              int64
	Username, FirstName string
	Uploads             int64
	Bytes               int64
	LastSeen            time.Time
}
type Stats struct {
	Files, Users int64
	Bytes        int64
	Expired      int64
}
type Store struct{ db *pgxpool.Pool }

func Open(ctx context.Context, dsn string) (*Store, error) {
	db, err := pgxpool.New(ctx, dsn)
	if err != nil {
		return nil, err
	}
	if err = db.Ping(ctx); err != nil {
		db.Close()
		return nil, err
	}
	_, err = db.Exec(ctx, `CREATE TABLE IF NOT EXISTS users (user_id BIGINT PRIMARY KEY, username TEXT NOT NULL DEFAULT '', first_name TEXT NOT NULL DEFAULT '', uploads BIGINT NOT NULL DEFAULT 0, bytes BIGINT NOT NULL DEFAULT 0, last_seen TIMESTAMPTZ NOT NULL);
CREATE TABLE IF NOT EXISTS files (token TEXT PRIMARY KEY, original_name TEXT NOT NULL, disk_name TEXT NOT NULL, mime_type TEXT NOT NULL DEFAULT '', size BIGINT NOT NULL, chat_id BIGINT NOT NULL, user_id BIGINT NOT NULL, message_id BIGINT NOT NULL, created_at TIMESTAMPTZ NOT NULL, expires_at TIMESTAMPTZ NULL, active BOOLEAN NOT NULL DEFAULT TRUE);
CREATE INDEX IF NOT EXISTS idx_files_created ON files(created_at DESC); CREATE INDEX IF NOT EXISTS idx_files_user ON files(user_id); CREATE INDEX IF NOT EXISTS idx_files_name ON files USING gin(to_tsvector('simple', original_name));`)
	return &Store{db: db}, err
}
func (s *Store) Close() { s.db.Close() }
func (s *Store) UpsertUser(ctx context.Context, u UserRecord) error {
	_, e := s.db.Exec(ctx, `INSERT INTO users(user_id,username,first_name,uploads,bytes,last_seen) VALUES($1,$2,$3,0,0,$4) ON CONFLICT(user_id) DO UPDATE SET username=EXCLUDED.username,first_name=EXCLUDED.first_name,last_seen=EXCLUDED.last_seen`, u.UserID, u.Username, u.FirstName, u.LastSeen)
	return e
}
func (s *Store) Add(ctx context.Context, r FileRecord) error {
	_, e := s.db.Exec(ctx, `INSERT INTO files(token,original_name,disk_name,mime_type,size,chat_id,user_id,message_id,created_at,expires_at,active) VALUES($1,$2,$3,$4,$5,$6,$7,$8,$9,NULLIF($10,'0001-01-01 00:00:00+00'::timestamptz),$11)`, r.Token, r.OriginalName, r.DiskName, r.MimeType, r.Size, r.ChatID, r.UserID, r.MessageID, r.CreatedAt, r.ExpiresAt, r.Active)
	if e == nil {
		_, e = s.db.Exec(ctx, `UPDATE users SET uploads=uploads+1,bytes=bytes+$2,last_seen=$3 WHERE user_id=$1`, r.UserID, r.Size, r.CreatedAt)
	}
	return e
}
func (s *Store) Get(ctx context.Context, token string) (FileRecord, bool, error) {
	var r FileRecord
	e := s.db.QueryRow(ctx, `SELECT token,original_name,disk_name,mime_type,size,chat_id,user_id,message_id,created_at,COALESCE(expires_at,'1970-01-01'::timestamptz),active FROM files WHERE token=$1 AND active=TRUE AND (expires_at IS NULL OR expires_at>NOW())`, token).Scan(&r.Token, &r.OriginalName, &r.DiskName, &r.MimeType, &r.Size, &r.ChatID, &r.UserID, &r.MessageID, &r.CreatedAt, &r.ExpiresAt, &r.Active)
	if errors.Is(e, pgx.ErrNoRows) {
		return r, false, nil
	}
	if e != nil {
		return r, false, e
	}
	return r, true, nil
}
func (s *Store) Delete(ctx context.Context, token string) (FileRecord, bool, error) {
	var r FileRecord
	e := s.db.QueryRow(ctx, `DELETE FROM files WHERE token=$1 RETURNING token,original_name,disk_name,mime_type,size,chat_id,user_id,message_id,created_at,COALESCE(expires_at,'1970-01-01'::timestamptz),active`, token).Scan(&r.Token, &r.OriginalName, &r.DiskName, &r.MimeType, &r.Size, &r.ChatID, &r.UserID, &r.MessageID, &r.CreatedAt, &r.ExpiresAt, &r.Active)
	if e != nil {
		return r, false, nil
	}
	_, e = s.db.Exec(ctx, `UPDATE users SET uploads=GREATEST(uploads-1,0),bytes=GREATEST(bytes-$2,0) WHERE user_id=$1`, r.UserID, r.Size)
	return r, true, e
}
func (s *Store) Search(ctx context.Context, q string, limit, offset int) ([]FileRecord, error) {
	rows, e := s.db.Query(ctx, `SELECT token,original_name,disk_name,mime_type,size,chat_id,user_id,message_id,created_at,COALESCE(expires_at,'1970-01-01'::timestamptz),active FROM files WHERE ($1='' OR original_name ILIKE '%'||$1||'%' OR token ILIKE '%'||$1||'%') ORDER BY created_at DESC LIMIT $2 OFFSET $3`, q, limit, offset)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []FileRecord{}
	for rows.Next() {
		var r FileRecord
		if e = rows.Scan(&r.Token, &r.OriginalName, &r.DiskName, &r.MimeType, &r.Size, &r.ChatID, &r.UserID, &r.MessageID, &r.CreatedAt, &r.ExpiresAt, &r.Active); e != nil {
			return nil, e
		}
		out = append(out, r)
	}
	return out, rows.Err()
}
func (s *Store) Users(ctx context.Context, limit, offset int) ([]UserRecord, error) {
	rows, e := s.db.Query(ctx, `SELECT user_id,username,first_name,uploads,bytes,last_seen FROM users ORDER BY last_seen DESC LIMIT $1 OFFSET $2`, limit, offset)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []UserRecord{}
	for rows.Next() {
		var u UserRecord
		if e = rows.Scan(&u.UserID, &u.Username, &u.FirstName, &u.Uploads, &u.Bytes, &u.LastSeen); e != nil {
			return nil, e
		}
		out = append(out, u)
	}
	return out, rows.Err()
}
func (s *Store) Stats(ctx context.Context) (Stats, error) {
	var x Stats
	e := s.db.QueryRow(ctx, `SELECT COUNT(*),COALESCE(SUM(size),0),COUNT(DISTINCT user_id) FROM files WHERE active=TRUE`).Scan(&x.Files, &x.Bytes, &x.Users)
	return x, e
}
func (s *Store) DeleteExpired(ctx context.Context) ([]FileRecord, error) {
	rows, e := s.db.Query(ctx, `DELETE FROM files WHERE expires_at IS NOT NULL AND expires_at<=NOW() RETURNING token,original_name,disk_name,size,user_id`)
	if e != nil {
		return nil, e
	}
	defer rows.Close()
	out := []FileRecord{}
	for rows.Next() {
		var r FileRecord
		if e = rows.Scan(&r.Token, &r.OriginalName, &r.DiskName, &r.Size, &r.UserID); e != nil {
			return nil, e
		}
		_, _ = s.db.Exec(ctx, `UPDATE users SET uploads=GREATEST(uploads-1,0),bytes=GREATEST(bytes-$2,0) WHERE user_id=$1`, r.UserID, r.Size)
		out = append(out, r)
	}
	return out, rows.Err()
}
