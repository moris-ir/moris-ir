package queue

import (
	"context"
	"encoding/json"
	"github.com/redis/go-redis/v9"
	"moris/connection"
)

type Queue struct {
	r   *redis.Client
	key string
}

func New(ctx context.Context, raw, key string) (*Queue, error) {
	o, e := redis.ParseURL(raw)
	if e != nil {
		return nil, e
	}
	r := redis.NewClient(o)
	if e = r.Ping(ctx).Err(); e != nil {
		return nil, e
	}
	return &Queue{r: r, key: key}, nil
}
func (q *Queue) Close() error { return q.r.Close() }
func (q *Queue) Push(ctx context.Context, m *connection.Message) error {
	b, e := json.Marshal(m)
	if e != nil {
		return e
	}
	return q.r.RPush(ctx, q.key, b).Err()
}
func (q *Queue) Pop(ctx context.Context) (*connection.Message, error) {
	v, e := q.r.BLPop(ctx, 0, q.key).Result()
	if e != nil {
		return nil, e
	}
	var m connection.Message
	e = json.Unmarshal([]byte(v[1]), &m)
	return &m, e
}
func (q *Queue) Depth(ctx context.Context) (int64, error) { return q.r.LLen(ctx, q.key).Result() }
