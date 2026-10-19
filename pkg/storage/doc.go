// Package storage 提供模块化对象存储：统一契约 + 可插拔多云驱动。
//
// 设计边界（对齐 SEC / REL / 架构基础设施适配）：
//   - 业务只依赖 Store / Presigner；通过 storage.Open + Driver 切换后端
//   - 内置 memory / local；云驱动为可选子包：s3、oss、cos、obs（blank-import 注册）
//   - 阿里云 / 腾讯云 / 华为云 / AWS / MinIO 等均为可选项；L1 优先 S3 兼容协议
//   - Key 须含租户等隔离维度；禁止路径穿越；日志不得打印签名 URL 密钥查询串（SEC-05）
//   - Put 可配置大小上限（REL）；Presign 由支持的驱动实现
//
// 典型用法：
//
//	import _ "github.com/admin-frame/admin-frame/pkg/storage/s3"
//	import _ "github.com/admin-frame/admin-frame/pkg/storage/oss"
//
//	store, err := storage.Open(ctx, storage.Config{Driver: "oss", S3: storage.S3Config{...}})
//	info, err := store.Put(ctx, storage.MustJoin("m1", "a.pdf"), body, storage.PutOptions{})
//
// 云驱动规划见 docs/storage-s3-oss.md。
package storage
