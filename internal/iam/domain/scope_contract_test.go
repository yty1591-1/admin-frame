package domain_test

import (
	"testing"

	"github.com/admin-frame/admin-frame/internal/iam/domain"
)

// TestVerifyScopeContract 锁定数据范围字面值与 shared/authz 契约一致。
//
// 该断言同时在包 init 中执行：任一侧改名而另一侧未同步，进程起不来。
// 本用例把它显式化为可读的回归信号（评审差距 G5）。
func TestVerifyScopeContract(t *testing.T) {
	if err := domain.VerifyScopeContract(); err != nil {
		t.Fatalf("数据范围契约漂移：%v", err)
	}
}
