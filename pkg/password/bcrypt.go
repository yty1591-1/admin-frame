package password

import (
	"fmt"

	"golang.org/x/crypto/bcrypt"
)

// BcryptParams 控制 bcrypt 成本。
type BcryptParams struct {
	// Cost 工作因子；0 表示 DefaultBcryptCost。合法范围 [bcrypt.MinCost, bcrypt.MaxCost]。
	Cost int
}

// DefaultBcryptCost 生产推荐成本（高于库默认 10）。
const DefaultBcryptCost = 12

func (p BcryptParams) normalize() (BcryptParams, error) {
	if p.Cost == 0 {
		p.Cost = DefaultBcryptCost
	}
	if p.Cost < bcrypt.MinCost || p.Cost > bcrypt.MaxCost {
		return p, fmt.Errorf("%w: bcrypt cost %d out of range", ErrInvalidConfig, p.Cost)
	}
	return p, nil
}

func hashBcrypt(plain []byte, p BcryptParams) (string, error) {
	p, err := p.normalize()
	if err != nil {
		return "", err
	}
	if err := checkPassword(plain, BcryptMaxBytes); err != nil {
		return "", err
	}
	out, err := bcrypt.GenerateFromPassword(plain, p.Cost)
	if err != nil {
		return "", fmt.Errorf("password: bcrypt hash: %w", err)
	}
	return string(out), nil
}

func verifyBcrypt(encoded string, plain []byte) error {
	if err := checkPassword(plain, BcryptMaxBytes); err != nil {
		// 超长对 bcrypt 恒为不匹配，避免与「空密码」语义混淆时：空仍返回 ErrEmptyPassword
		if err == ErrPasswordTooLong {
			return ErrMismatch
		}
		return err
	}
	err := bcrypt.CompareHashAndPassword([]byte(encoded), plain)
	if err == nil {
		return nil
	}
	if err == bcrypt.ErrMismatchedHashAndPassword {
		return ErrMismatch
	}
	return ErrInvalidHash
}

func bcryptNeedsRehash(encoded string, p BcryptParams) bool {
	p, err := p.normalize()
	if err != nil {
		return true
	}
	cost, err := bcrypt.Cost([]byte(encoded))
	if err != nil {
		return true
	}
	return cost != p.Cost
}
