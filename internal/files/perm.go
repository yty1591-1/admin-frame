package files

// 文件域权限点（rbac.Permission 字符串）。
//
// 与 order 域同样放在域根包，由 cmd 授予角色，平台层不持有权限数据。
const (
	// PermRead 读取/下载附件。
	PermRead = "file:read"
	// PermWrite 上传/预签名上传附件。
	PermWrite = "file:write"
)
