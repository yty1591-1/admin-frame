package spec

import "strings"

// MissingStorageRefs 返回对象存储条件必填仍缺值的键（namespace/key）。
//
// 仅在 storage.driver=s3/minio 时要求 bucket/region：KeySpec.Required 表达不了
// 跨键依赖，安装态 Gaps 与进程侧 configboot.MissingRequired 共用本函数，避免
// 「install ready 但业务进程仍缺桶参数」的分裂判定（ADR-0018）。
func MissingStorageRefs(values map[string]string) []string {
	if values == nil {
		return nil
	}
	driver := strings.ToLower(strings.TrimSpace(values["storage.driver"]))
	if driver != "s3" && driver != "minio" {
		return nil
	}
	refs := make([]string, 0, 2)
	for _, key := range []string{"storage.s3.bucket", "storage.s3.region"} {
		if strings.TrimSpace(values[key]) == "" {
			refs = append(refs, NamespacePlatform+"/"+key)
		}
	}
	return refs
}
