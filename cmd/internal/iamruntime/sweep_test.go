package iamruntime_test

import (
	"context"
	"testing"

	"github.com/admin-frame/admin-frame/cmd/internal/iamruntime"
	"github.com/admin-frame/admin-frame/internal/iam/app"
	iamconfig "github.com/admin-frame/admin-frame/internal/iam/config"
	platformconfig "github.com/admin-frame/admin-frame/internal/platform/config"
)

// TestBuildCertificationSweeperDisabled 验证显式关闭访问复核时不构造扫描用例。
func TestBuildCertificationSweeperDisabled(t *testing.T) {
	deps := newDeps(t, platformconfig.AsyncConfig{}, false)
	disabled := false
	cfg := iamTestConfig()
	cfg.Certification = iamconfig.IAMCertificationConfig{Enabled: &disabled}

	svc, err := iamruntime.BuildCertificationSweeper(deps, cfg)
	if err != nil {
		t.Fatalf("BuildCertificationSweeper: %v", err)
	}
	if svc != nil {
		t.Fatal("disabled certification must not build a sweeper")
	}
}

// TestBuildCertificationSweeperRuns 验证启用时可用内存仓储跑完一轮空扫描。
func TestBuildCertificationSweeperRuns(t *testing.T) {
	deps := newDeps(t, platformconfig.AsyncConfig{}, false)
	svc, err := iamruntime.BuildCertificationSweeper(deps, iamTestConfig())
	if err != nil {
		t.Fatalf("BuildCertificationSweeper: %v", err)
	}
	if svc == nil {
		t.Fatal("default certification config must build a sweeper")
	}
	result, err := svc.SweepOverdue(context.Background())
	if err != nil {
		t.Fatalf("SweepOverdue: %v", err)
	}
	if result != (app.CertificationSweepResult{}) {
		t.Fatalf("empty sweep result=%+v, want zero value", result)
	}
}

// TestBuildCertificationSweeperRequiresIDs 验证缺失 ID 生成器时失败关闭。
func TestBuildCertificationSweeperRequiresIDs(t *testing.T) {
	deps := newDeps(t, platformconfig.AsyncConfig{}, false)
	deps.IDs = nil
	if _, err := iamruntime.BuildCertificationSweeper(deps, iamTestConfig()); err == nil {
		t.Fatal("missing id generator must fail closed")
	}
}
