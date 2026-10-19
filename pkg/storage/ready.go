package storage

import "context"

// Ready 轻量就绪探测：List 一条（空前缀）。用于 /readyz。
func Ready(ctx context.Context, s Store) error {
	if s == nil {
		return ErrClosed
	}
	_, err := s.List(ctx, ListOptions{Limit: 1})
	return err
}
