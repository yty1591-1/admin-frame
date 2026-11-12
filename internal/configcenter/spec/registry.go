// Package spec 维护配置键的代码声明注册表（CFG-07 / CC-F-41）。
//
// 所有可写配置键必须在装配期通过 Register / MustRegister 声明；注册表是
// 管理面创建、首次启动播种默认值与双读校验的唯一事实源。未声明的键拒绝
// 写入，声明冲突（重复）在启动期即失败。
package spec

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/admin-frame/admin-frame/internal/configcenter/domain"
)

// Registry 是并发安全的键声明注册表；按 namespace 隔离。
type Registry struct {
	mu    sync.RWMutex
	specs map[string]domain.KeySpec
}

// NewRegistry 构造空注册表。
func NewRegistry() *Registry {
	return &Registry{specs: make(map[string]domain.KeySpec)}
}

// Register 校验并登记一组声明；任一非法或重复则整批拒绝（不留部分状态）。
func (r *Registry) Register(specs ...domain.KeySpec) error {
	pending := make(map[string]domain.KeySpec, len(specs))
	for _, keySpec := range specs {
		if err := keySpec.Validate(); err != nil {
			return err
		}
		id := specID(keySpec.Namespace, keySpec.Key)
		if _, dup := pending[id]; dup {
			return fmt.Errorf("configcenter: duplicate key spec %s/%s", keySpec.Namespace, keySpec.Key)
		}
		r.mu.RLock()
		_, exists := r.specs[id]
		r.mu.RUnlock()
		if exists {
			return fmt.Errorf("configcenter: key spec %s/%s is already registered", keySpec.Namespace, keySpec.Key)
		}
		pending[id] = keySpec
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	for id, keySpec := range pending {
		r.specs[id] = keySpec
	}
	return nil
}

// MustRegister 在装配期登记声明，失败直接 panic（fail fast）。
func (r *Registry) MustRegister(specs ...domain.KeySpec) {
	if err := r.Register(specs...); err != nil {
		panic(err)
	}
}

// Lookup 返回指定 namespace + key 的声明。
func (r *Registry) Lookup(namespace, key string) (domain.KeySpec, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	keySpec, ok := r.specs[specID(namespace, key)]
	return keySpec, ok
}

// List 返回 namespace 下全部声明，按 key 字典序排序。
func (r *Registry) List(namespace string) []domain.KeySpec {
	r.mu.RLock()
	defer r.mu.RUnlock()
	listed := make([]domain.KeySpec, 0, len(r.specs))
	for _, keySpec := range r.specs {
		if keySpec.Namespace == namespace {
			listed = append(listed, keySpec)
		}
	}
	sort.Slice(listed, func(i, j int) bool { return listed[i].Key < listed[j].Key })
	return listed
}

// Namespaces 返回已声明键的全部 namespace，按字典序排序。
func (r *Registry) Namespaces() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	seen := make(map[string]struct{}, len(r.specs))
	namespaces := make([]string, 0, len(r.specs))
	for _, keySpec := range r.specs {
		if _, ok := seen[keySpec.Namespace]; ok {
			continue
		}
		seen[keySpec.Namespace] = struct{}{}
		namespaces = append(namespaces, keySpec.Namespace)
	}
	sort.Strings(namespaces)
	return namespaces
}

// Defaults 返回 namespace 下所有非空代码默认值（首次启动播种输入）。
//
// 敏感键与 Required 键没有默认值，不会出现在结果中。
func (r *Registry) Defaults(namespace string) map[string]string {
	r.mu.RLock()
	defer r.mu.RUnlock()
	defaults := make(map[string]string)
	for _, keySpec := range r.specs {
		if keySpec.Namespace != namespace || keySpec.Default == "" {
			continue
		}
		defaults[keySpec.Key] = keySpec.Default
	}
	return defaults
}

// ValidateValues 校验一份完整的 namespace 取值集合：
//   - 每个 key 必须已声明（未声明的键拒绝写入）；
//   - 声明为 Required 的键必须有值；
//   - 每个值必须通过声明中的类型 / 枚举 / 区间 / 正则校验。
//
// 返回的错误聚合全部问题，便于管理面一次性提示。
func (r *Registry) ValidateValues(namespace string, values map[string]string) error {
	errs := make([]string, 0, 4)
	for key := range values {
		if _, ok := r.Lookup(namespace, key); !ok {
			errs = append(errs, fmt.Sprintf("key %q is not declared in code key spec", key))
		}
	}
	r.mu.RLock()
	defer r.mu.RUnlock()
	for _, keySpec := range r.specs {
		if keySpec.Namespace != namespace {
			continue
		}
		value, present := values[keySpec.Key]
		if keySpec.Required && (!present || value == "") {
			errs = append(errs, fmt.Sprintf("required key %q is missing", keySpec.Key))
			continue
		}
		if !present || value == "" {
			continue
		}
		if err := keySpec.ValidateValue(value); err != nil {
			errs = append(errs, fmt.Sprintf("key %q: %v", keySpec.Key, err))
		}
	}
	if len(errs) > 0 {
		return fmt.Errorf("configcenter: key spec validation failed: %s", strings.Join(errs, "; "))
	}
	return nil
}

// specID 拼接注册表键；使用 NUL 分隔避免 namespace/key 边界歧义。
func specID(namespace, key string) string {
	return namespace + "\x00" + key
}
