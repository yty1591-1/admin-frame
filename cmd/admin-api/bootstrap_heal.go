package main

import (
	"context"
	"log"
	"os"
	"strings"

	"github.com/admin-frame/admin-frame/cmd/internal/configboot"
)

// startupHeal 承载启动期自愈的结果（ADR-0018 决策 3 / 待定 1）。
type startupHeal struct {
	// Boot 是重载后的启动结果；未发生重载时与入参相同。
	Boot *configboot.Result
	// Gaps 非空表示判定为「缺值阻塞」，调用方应转 setup-only。
	Gaps []string
	// ResetRefs 是本次诊断出的、按声明默认值重置的键（namespace/key）。
	ResetRefs []string
	// Reloaded 为 true 表示重置成功且已重新拉取配置，调用方可以重试装配。
	Reloaded bool
}

// healStartupConfig 处理「装配后校验仍失败」的两类可自愈故障：
//
//  1. 填占位值就能通过校验 → 判定为缺值（Gaps 非空），调用方转 setup-only；
//  2. 只有把取值改回声明默认值才能通过 → 判定为值非法，调内部自愈端点后重载。
//
// 两者都判不出时返回零值，调用方保持 fail-fast：宁可起不来，也不带病运行或
// 顺手改掉一批无关键。reload 与 parse 由调用方注入，生产分别是 configboot.Load
// 与 appconfig.LoadFromValues；集成测试用 config-api 桩与精简校验替换后者，
// 从而在不装配整套平台的前提下覆盖「诊断 → 重置 → 重载」链路。
func healStartupConfig(
	ctx context.Context,
	boot *configboot.Result,
	values map[string]string,
	cause error,
	parse func(map[string]string) error,
	reload func(context.Context) (*configboot.Result, error),
) startupHeal {
	if gaps, ok := configboot.DiagnoseMissingValues(values, parse); ok {
		return startupHeal{Boot: boot, Gaps: gaps}
	}
	refs, ok := configboot.DiagnoseInvalidValues(values, parse)
	if !ok {
		return startupHeal{Boot: boot}
	}
	log.Printf("admin-api: config validation failed (%v); resetting invalid values to declared defaults: %s",
		cause, strings.Join(refs, ", "))
	identity := strings.TrimSpace(os.Getenv("CONFIG_SERVICE_IDENTITY"))
	if err := configboot.ResetInvalidValues(ctx, boot.ConfigAPIURL, boot.HTTPClient, identity, boot.Env, refs); err != nil {
		log.Printf("admin-api: reset invalid values failed: %v", err)
		return startupHeal{Boot: boot, ResetRefs: refs}
	}
	reloaded, err := reload(ctx)
	if err != nil {
		log.Printf("admin-api: reload config after reset failed: %v", err)
		return startupHeal{Boot: boot, ResetRefs: refs}
	}
	return startupHeal{Boot: reloaded, ResetRefs: refs, Reloaded: true}
}
