package pagination

import "net/http"

// Params 分页 + 排序合一结果。
type Params struct {
	Query Query
	Sort  []Order
}

// ParseParams 同时解析分页与排序。
func ParseParams(r *http.Request, pageCfg Config, sortCfg SortConfig) (Params, error) {
	q, err := ParseWith(r, pageCfg)
	if err != nil {
		return Params{}, err
	}
	sorts, err := ParseSort(r, sortCfg)
	if err != nil {
		return Params{}, err
	}
	return Params{Query: q, Sort: sorts}, nil
}
