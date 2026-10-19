package mq

import (
	"context"
	"time"
)

// OutboxRecord 事务外盒记录（与本地业务事务同库写入）。
type OutboxRecord struct {
	// ID 记录主键（UUID 或雪花）。
	ID string
	// Message 待发布信封。
	Message Message
	// CreatedAt 入盒时间。
	CreatedAt time.Time
	// PublishedAt 成功发布时间；零值表示待发布。
	PublishedAt time.Time
}

// OutboxStore 外盒持久化抽象；具体 SQL 实现放 infrastructure，禁止 API/worker 启动时 migrate。
type OutboxStore interface {
	Enqueue(ctx context.Context, rec OutboxRecord) error
	// FetchPending 拉取未发布记录，按 CreatedAt 升序。
	FetchPending(ctx context.Context, limit int) ([]OutboxRecord, error)
	MarkPublished(ctx context.Context, id string, at time.Time) error
}

// OutboxRelay 将外盒记录可靠发布到 Producer。
type OutboxRelay struct {
	Store    OutboxStore
	Producer Producer
	// BatchSize 每轮拉取条数；≤0 表示 32。
	BatchSize int
	// IdleWait 无数据时休眠；≤0 表示 500ms。
	IdleWait time.Duration
}

// Run 循环拉取并 Publish，直至 ctx 取消。
func (r *OutboxRelay) Run(ctx context.Context) error {
	if r == nil || r.Store == nil || r.Producer == nil {
		return ErrInvalidConfig
	}
	batch := r.BatchSize
	if batch <= 0 {
		batch = 32
	}
	idle := r.IdleWait
	if idle <= 0 {
		idle = 500 * time.Millisecond
	}

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		n, err := r.relayOnce(ctx, batch)
		if err != nil {
			return err
		}
		if n == 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(idle):
			}
		}
	}
}

func (r *OutboxRelay) relayOnce(ctx context.Context, batch int) (int, error) {
	recs, err := r.Store.FetchPending(ctx, batch)
	if err != nil {
		return 0, err
	}
	for _, rec := range recs {
		if err := r.Producer.Publish(ctx, rec.Message); err != nil {
			return 0, err
		}
		if err := r.Store.MarkPublished(ctx, rec.ID, time.Now().UTC()); err != nil {
			return 0, err
		}
	}
	return len(recs), nil
}
