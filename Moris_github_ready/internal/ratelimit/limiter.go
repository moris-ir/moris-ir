package ratelimit

import (
	"sync"
	"time"
)

type Limiter struct {
	mu      sync.Mutex
	limit   int
	window  time.Duration
	history map[int64][]time.Time
}

func New(limit int) *Limiter {
	return &Limiter{limit: limit, window: time.Minute, history: make(map[int64][]time.Time)}
}

func (l *Limiter) Allow(userID int64) bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	cut := now.Add(-l.window)
	old := l.history[userID]
	i := 0
	for i < len(old) && old[i].After(cut) {
		i++
	}
	old = old[i:]
	if len(old) >= l.limit {
		l.history[userID] = old
		return false
	}
	l.history[userID] = append(old, now)
	return true
}
