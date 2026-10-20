// Package config 提供类型安全的配置加载、环境变量覆盖、校验与热更新原语。
//
// 加载顺序固定为：默认值 → 配置文件（按声明顺序依次覆盖）→ 环境变量 → 校验
// （对齐 CONFIG_STANDARDS CFG-04：环境变量 > 配置文件 > 默认值）。
//
// 设计边界：
//   - 不依赖 internal/，不感知具体业务字段（CFG-07）
//   - 不读取任何默认路径、不隐式读取环境变量名；全部由 Options 显式声明
//   - 不打印、不回显配置值；错误只包含文件路径、字段路径与环境变量名，
//     避免密钥/DSN 经启动日志泄漏（SEC-05）
//   - 文件读取与解析在启动期完成；热更新由 Watcher 轮询并原子替换快照
//
// YAML 采用严格模式：未知字段直接失败，防止拼写错误被静默忽略。
// 需要 JSON 等格式时通过 Options.Decode 注入，不改变其余语义。
//
// 并发：Loader 构造后只读，可并发调用 Load；Watcher.Current 返回指向不可变
// 快照的指针，调用方不得修改；Watcher 的订阅回调在监视 goroutine 内同步执行，
// 必须快速返回且不得阻塞。
//
// 零值：Loader / Watcher 的零值不可用，必须分别经 NewLoader / NewWatcher 构造。
//
// 典型用法：
//
//	loader, err := config.NewLoader(config.Options[AppConfig]{
//		Files:     []string{"configs/admin.yaml"},
//		EnvPrefix: "ADMIN_FRAME",
//		Defaults:  applyDefaults,
//		Validate:  func(c *AppConfig) error { return c.Validate() },
//	})
//	if err != nil {
//		return err
//	}
//	cfg, err := loader.Load()
//	if err != nil {
//		return err
//	}
//
// 热更新：
//
//	watcher, err := config.NewWatcher(config.WatchOptions[AppConfig]{Loader: loader})
//	if err != nil {
//		return err
//	}
//	watcher.Subscribe(func(ev config.Event[AppConfig]) {
//		if ev.Err != nil {
//			slog.Error("config reload failed", "err", ev.Err)
//			return
//		}
//		applyDynamicSettings(ev.New)
//	})
//	if err := watcher.Start(); err != nil {
//		return err
//	}
//	defer watcher.Close()
package config
