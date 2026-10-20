package convert

// Ptr 返回 v 的指针；每次调用都会分配新的变量。
func Ptr[T any](v T) *T { return &v }

// Value 解引用 p；p 为 nil 时返回 T 的零值。
func Value[T any](p *T) T {
	if p == nil {
		var zero T
		return zero
	}
	return *p
}

// ValueOr 解引用 p；p 为 nil 时返回 def。
func ValueOr[T any](p *T, def T) T {
	if p == nil {
		return def
	}
	return *p
}

// PtrIf 在 ok 为 true 时返回 v 的指针，否则返回 nil。
func PtrIf[T any](v T, ok bool) *T {
	if !ok {
		return nil
	}
	return &v
}

// NilIfZero 在 v 等于 T 的零值时返回 nil，否则返回 v 的指针。
func NilIfZero[T comparable](v T) *T {
	var zero T
	if v == zero {
		return nil
	}
	return &v
}

// Clone 复制 p 指向的值；p 为 nil 时返回 nil。浅拷贝，不复制引用类型的底层数据。
func Clone[T any](p *T) *T {
	if p == nil {
		return nil
	}
	v := *p
	return &v
}
