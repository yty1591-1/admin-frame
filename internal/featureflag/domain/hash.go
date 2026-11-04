package domain

import (
	"crypto/sha256"
	"encoding/binary"
)

// Bucket 计算 (flag_key, subject_id) 的稳定分桶序号：SHA-256 摘要前 8 字节
// 取模 bucketCount。纯计算无 I/O，归 domain 供评估引擎直接使用（便于
// 属性测试与替换）。
//
// 性质（feature-flag-plan.md §0.4 / FF-02 属性测试）：
//   - 同 (key, subject) 恒返回同序号——配比不变时命中结果稳定，不闪断；
//   - 阈值上调只增不减——分桶序号与主体绑定，与配比无关；
//   - 均匀性——SHA-256 输出近似均匀，分桶偏差在万分比精度内可忽略。
func Bucket(flagKey, subjectID string, bucketCount int) int {
	// bucketCount 由调用方传入正数（评估引擎传 domain.MaxPercentage）；
	// 防御负值：与 0 取模会 panic，负值语义退化为 1。
	count := bucketCount
	if count <= 0 {
		count = 1
	}
	sum := sha256.Sum256([]byte(flagKey + ":" + subjectID))
	return int(binary.BigEndian.Uint64(sum[:8]) % uint64(count)) //nolint:gosec // G115: count>0 且取模结果恒 < count，转换无损。
}
