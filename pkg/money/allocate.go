package money

import (
	"fmt"
	"math/big"
	"sort"
)

// Allocate 按权重把金额拆分给多个接收方，保证结果之和等于原金额。
//
// 采用最大余数法：先按比例向下取整，再把剩余最小单位依次分配给余数最大的项；
// 余数相同时按输入顺序优先。权重必须非负且至少一个为正（金额为 0 时允许全零）。
// 返回切片长度与 weights 一致。
func (m Money) Allocate(weights ...int64) ([]Money, error) {
	if _, ok := CurrencyExponent(m.currency); !ok {
		return nil, fmt.Errorf("%w: %q", ErrUnknownCurrency, m.currency)
	}
	if len(weights) == 0 {
		return nil, fmt.Errorf("%w: empty weights", ErrInvalidAllocation)
	}
	totalWeight := new(big.Int)
	for _, w := range weights {
		if w < 0 {
			return nil, fmt.Errorf("%w: negative weight %d", ErrInvalidAllocation, w)
		}
		totalWeight.Add(totalWeight, big.NewInt(w))
	}
	if totalWeight.Sign() == 0 {
		if m.amountMinor != 0 {
			return nil, fmt.Errorf("%w: all weights are zero", ErrInvalidAllocation)
		}
		out := make([]Money, len(weights))
		for i := range out {
			out[i] = Money{currency: m.currency}
		}
		return out, nil
	}

	magnitude := new(big.Int).Abs(big.NewInt(m.amountMinor))
	type share struct {
		quotient  *big.Int
		remainder *big.Int
	}
	shares := make([]share, len(weights))
	sum := new(big.Int)
	for i, w := range weights {
		product := new(big.Int).Mul(magnitude, big.NewInt(w))
		q := new(big.Int)
		r := new(big.Int)
		q.QuoRem(product, totalWeight, r)
		shares[i] = share{quotient: q, remainder: r}
		sum.Add(sum, q)
	}

	leftover := new(big.Int).Sub(magnitude, sum).Int64()
	order := make([]int, len(weights))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool {
		cmp := shares[order[i]].remainder.Cmp(shares[order[j]].remainder)
		if cmp != 0 {
			return cmp > 0
		}
		return order[i] < order[j]
	})
	one := big.NewInt(1)
	for i := 0; i < int(leftover); i++ {
		shares[order[i]].quotient.Add(shares[order[i]].quotient, one)
	}

	out := make([]Money, len(weights))
	for i, s := range shares {
		value := s.quotient
		if m.amountMinor < 0 {
			value = new(big.Int).Neg(value)
		}
		if !value.IsInt64() {
			return nil, fmt.Errorf("%w: share %s", ErrOverflow, value)
		}
		out[i] = Money{amountMinor: value.Int64(), currency: m.currency}
	}
	return out, nil
}

// Split 把金额等额拆分为 n 份；无法整除的余数按输入顺序优先分配。
func (m Money) Split(n int) ([]Money, error) {
	if n <= 0 {
		return nil, fmt.Errorf("%w: split count %d", ErrInvalidAllocation, n)
	}
	weights := make([]int64, n)
	for i := range weights {
		weights[i] = 1
	}
	return m.Allocate(weights...)
}
