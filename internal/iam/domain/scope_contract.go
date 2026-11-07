package domain

import (
	"fmt"

	"github.com/admin-frame/admin-frame/internal/shared/authz"
)

// 契约对齐（评审差距 G5）：shared/authz 的 Scope 常量与 iam/domain 的
// DataScopeType 字面值必须逐字一致——权限快照跨进程 / 跨缓存序列化时，
// 两侧靠字面值互认，一旦改名而另一侧未同步，数据范围会静默降级为「无范围」。
//
// Go 无法对字符串做编译期比较，因此用 init 断言把「不一致」变成
// 进程启动失败 / 测试立刻失败，而不是运行期鉴权范围悄悄失效。
func init() {
	if err := VerifyScopeContract(); err != nil {
		panic(err)
	}
}

// VerifyScopeContract 校验数据范围字面值在 domain 与 shared/authz 两侧一致。
//
// 新增数据范围类型时须同时登记到本函数的 pairs 列表，否则本契约不会覆盖它。
func VerifyScopeContract() error {
	pairs := []struct {
		name   string
		domain DataScopeType
		shared string
	}{
		{"all", DataScopeAll, authz.ScopeAll},
		{"merchant", DataScopeMerchant, authz.ScopeMerchant},
		{"self", DataScopeSelf, authz.ScopeSelf},
		{"merchant_set", DataScopeMerchantSet, authz.ScopeMerchantSet},
	}
	for _, p := range pairs {
		if string(p.domain) != p.shared {
			return fmt.Errorf(
				"数据范围契约不一致：internal/iam/domain 的 %s=%q，internal/shared/authz 为 %q（两侧字面值必须相同）",
				p.name, string(p.domain), p.shared)
		}
	}
	return nil
}
