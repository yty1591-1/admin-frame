package configcenterruntime

import (
	"hash/fnv"
	"strconv"

	platformconfig "github.com/admin-frame/admin-frame/internal/platform/config"
	"github.com/admin-frame/admin-frame/pkg/hotconfig"
	"github.com/admin-frame/admin-frame/pkg/ratelimit"
)

// RateLimitParams 返回入口限流的运行期参数来源（ADR-0015）。
//
// 取值优先级：热更视图 > 配置文件静态值。启动期视图为空或某个键尚未发布时，
// 对应字段回落到 static，因此首次发布前行为与改造前完全一致。
//
// 返回的版本号是四个参数的取值哈希：只有 `rate_limit.*` 变化才会触发重配置，
// 改 `log.level` 之类无关键不会让限流器做无谓的参数下发。
func (r *Runtime) RateLimitParams(static platformconfig.RateLimitConfig) ratelimit.ParamsProvider {
	if r == nil || r.Hot == nil {
		return nil
	}
	return &rateLimitSource{holder: r.Hot, static: static}
}

// rateLimitSource 把热更视图适配为 ratelimit.ParamsProvider。
type rateLimitSource struct {
	holder *hotconfig.Holder
	static platformconfig.RateLimitConfig
}

// Params 实现 ratelimit.ParamsProvider；视图永远可读，故 ok 恒为 true。
func (s *rateLimitSource) Params() (ratelimit.Params, int64, bool) {
	params := ratelimit.Params{
		RPS:              s.static.RPS,
		Burst:            s.static.Burst,
		IdleTTL:          s.static.IdleTTL,
		DegradeOnFailure: s.static.DegradeOnFailure,
	}
	view := s.holder.Current()
	if view != nil {
		if raw, ok := view.Lookup("rate_limit.rps"); ok {
			// 声明期用正则约束为十进制数字串，这里只做兜底解析。
			if parsed, err := strconv.ParseFloat(raw, 64); err == nil {
				params.RPS = parsed
			}
		}
		params.Burst = int(view.Int("rate_limit.burst", int64(params.Burst)))
		params.IdleTTL = view.Duration("rate_limit.idle_ttl", params.IdleTTL)
		params.DegradeOnFailure = view.Bool("rate_limit.degrade_on_failure", params.DegradeOnFailure)
	}
	return params, rateLimitVersion(params), true
}

// rateLimitVersion 把参数取值折成版本号：取值相同则版本相同。
func rateLimitVersion(params ratelimit.Params) int64 {
	hash := fnv.New64a()
	_, _ = hash.Write([]byte(strconv.FormatFloat(params.RPS, 'f', -1, 64)))
	_, _ = hash.Write([]byte{'|'})
	_, _ = hash.Write([]byte(strconv.Itoa(params.Burst)))
	_, _ = hash.Write([]byte{'|'})
	_, _ = hash.Write([]byte(params.IdleTTL.String()))
	_, _ = hash.Write([]byte{'|'})
	_, _ = hash.Write([]byte(strconv.FormatBool(params.DegradeOnFailure)))
	return int64(hash.Sum64() & 0x7fffffffffffffff)
}
