package app_test

import (
	"context"
	"fmt"
	"testing"

	"github.com/admin-frame/admin-frame/internal/iam"
	"github.com/admin-frame/admin-frame/internal/iam/app"
	"github.com/admin-frame/admin-frame/internal/iam/domain"
	"github.com/admin-frame/admin-frame/internal/shared/auth"
)

// benchSnapshot 构造指定权限数的快照，主体字段与查询保持一致。
func benchSnapshot(query domain.SubjectQuery, permissions int) *domain.SubjectSnapshot {
	codes := make([]iam.PermissionCode, 0, permissions)
	for i := 0; i < permissions; i++ {
		codes = append(codes, iam.PermissionCode(fmt.Sprintf("res%d:read", i)))
	}
	return &domain.SubjectSnapshot{
		Channel:     query.Channel,
		SubjectType: query.SubjectType,
		SubjectID:   query.SubjectID,
		MerchantID:  query.MerchantID,
		Roles:       []string{"bench_role"},
		Permissions: codes,
		Scopes:      []domain.DataScope{{Resource: "res0", Type: domain.DataScopeMerchant}},
	}
}

// benchService 构造回显查询条件的服务，避免主体不一致触发失败关闭。
func benchService(b *testing.B, permissions int) *app.AuthorizeService {
	b.Helper()
	repo := &fakeRepository{load: func(_ context.Context, query domain.SubjectQuery) (*domain.SubjectSnapshot, error) {
		return benchSnapshot(query, permissions), nil
	}}
	service, err := app.NewAuthorizeService(app.AuthorizeConfig{
		Repository:     repo,
		Cache:          newCache(b),
		Env:            "bench",
		LoadTimeout:    app.DefaultLoadTimeout,
		MaxRoles:       app.DefaultMaxRoles,
		MaxPermissions: app.DefaultMaxPermissions,
	})
	if err != nil {
		b.Fatalf("new service: %v", err)
	}
	return service
}

// BenchmarkAuthorize_CacheHit 测量 L1 命中路径（快照解码 + 清洗）。
func BenchmarkAuthorize_CacheHit(b *testing.B) {
	service := benchService(b, 10)
	principal := auth.Principal{Channel: auth.ChannelBiz, SubjectID: "u-1", MerchantID: "m-1"}
	if _, err := service.LoadSnapshot(context.Background(), principal); err != nil {
		b.Fatalf("warmup: %v", err)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := service.LoadSnapshot(context.Background(), principal); err != nil {
			b.Fatalf("load: %v", err)
		}
	}
}

// BenchmarkAuthorize_CacheMiss_DB 测量回源 + 回填路径（每个迭代使用不同主体）。
func BenchmarkAuthorize_CacheMiss_DB(b *testing.B) {
	service := benchService(b, 10)
	ctx := context.Background()
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		principal := auth.Principal{
			Channel:    auth.ChannelBiz,
			SubjectID:  fmt.Sprintf("u-%d", i),
			MerchantID: "m-1",
		}
		if _, err := service.LoadSnapshot(ctx, principal); err != nil {
			b.Fatalf("load: %v", err)
		}
	}
}

// BenchmarkSnapshotLoad_100Permissions 测量 100 权限快照的命中加载成本。
func BenchmarkSnapshotLoad_100Permissions(b *testing.B) {
	service := benchService(b, 100)
	principal := auth.Principal{Channel: auth.ChannelBiz, SubjectID: "u-100", MerchantID: "m-1"}
	if _, err := service.LoadSnapshot(context.Background(), principal); err != nil {
		b.Fatalf("warmup: %v", err)
	}
	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, err := service.LoadSnapshot(context.Background(), principal); err != nil {
			b.Fatalf("load: %v", err)
		}
	}
}
