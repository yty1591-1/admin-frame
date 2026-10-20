package password

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"math"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2idParams 对齐 OWASP 对 argon2id 的推荐（m=19456 KiB, t=2, p=1）。
type Argon2idParams struct {
	Time    uint32 // 迭代次数；0 → DefaultArgon2Time
	Memory  uint32 // KiB；0 → DefaultArgon2Memory
	Threads uint8  // 并行度；0 → DefaultArgon2Threads
	SaltLen uint32 // 盐长度；0 → 16
	KeyLen  uint32 // 派生密钥长度；0 → 32
}

const (
	// DefaultArgon2Time 默认迭代次数。
	DefaultArgon2Time = 2
	// DefaultArgon2Memory 默认内存（KiB）≈ 19 MiB。
	DefaultArgon2Memory = 19 * 1024
	// DefaultArgon2Threads 默认并行度。
	DefaultArgon2Threads = 1
	defaultArgon2SaltLen = 16
	defaultArgon2KeyLen  = 32
)

func (p Argon2idParams) normalize() (Argon2idParams, error) {
	if p.Time == 0 {
		p.Time = DefaultArgon2Time
	}
	if p.Memory == 0 {
		p.Memory = DefaultArgon2Memory
	}
	if p.Threads == 0 {
		p.Threads = DefaultArgon2Threads
	}
	if p.SaltLen == 0 {
		p.SaltLen = defaultArgon2SaltLen
	}
	if p.KeyLen == 0 {
		p.KeyLen = defaultArgon2KeyLen
	}
	if p.Time < 1 || p.Memory < 8*1024 || p.Threads < 1 || p.SaltLen < 8 || p.KeyLen < 16 {
		return p, fmt.Errorf("%w: argon2id params too weak or invalid", ErrInvalidConfig)
	}
	return p, nil
}

func hashArgon2id(plain []byte, p Argon2idParams) (string, error) {
	p, err := p.normalize()
	if err != nil {
		return "", err
	}
	if err := checkPassword(plain, MaxPasswordBytes); err != nil {
		return "", err
	}
	salt := make([]byte, p.SaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("password: argon2id salt: %w", err)
	}
	sum := argon2.IDKey(plain, salt, p.Time, p.Memory, p.Threads, p.KeyLen)
	return encodeArgon2id(p, salt, sum), nil
}

func encodeArgon2id(p Argon2idParams, salt, sum []byte) string {
	b64Salt := base64.RawStdEncoding.EncodeToString(salt)
	b64Hash := base64.RawStdEncoding.EncodeToString(sum)
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, p.Memory, p.Time, p.Threads, b64Salt, b64Hash)
}

type parsedArgon2id struct {
	params Argon2idParams
	salt   []byte
	hash   []byte
}

func parseArgon2id(encoded string) (parsedArgon2id, error) {
	// $argon2id$v=19$m=65536,t=3,p=2$salt$hash
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return parsedArgon2id{}, ErrInvalidHash
	}
	if !strings.HasPrefix(parts[2], "v=") {
		return parsedArgon2id{}, ErrInvalidHash
	}
	ver, err := strconv.Atoi(strings.TrimPrefix(parts[2], "v="))
	if err != nil || ver != argon2.Version {
		return parsedArgon2id{}, ErrInvalidHash
	}
	var mem, timeCost uint32
	var threads uint8
	for _, kv := range strings.Split(parts[3], ",") {
		switch {
		case strings.HasPrefix(kv, "m="):
			n, e := strconv.ParseUint(kv[2:], 10, 32)
			if e != nil {
				return parsedArgon2id{}, ErrInvalidHash
			}
			mem = uint32(n)
		case strings.HasPrefix(kv, "t="):
			n, e := strconv.ParseUint(kv[2:], 10, 32)
			if e != nil {
				return parsedArgon2id{}, ErrInvalidHash
			}
			timeCost = uint32(n)
		case strings.HasPrefix(kv, "p="):
			n, e := strconv.ParseUint(kv[2:], 10, 8)
			if e != nil {
				return parsedArgon2id{}, ErrInvalidHash
			}
			threads = uint8(n)
		default:
			return parsedArgon2id{}, ErrInvalidHash
		}
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[4])
	if err != nil {
		return parsedArgon2id{}, ErrInvalidHash
	}
	sum, err := base64.RawStdEncoding.DecodeString(parts[5])
	if err != nil {
		return parsedArgon2id{}, ErrInvalidHash
	}
	if len(salt) == 0 || len(sum) == 0 || mem == 0 || timeCost == 0 || threads == 0 {
		return parsedArgon2id{}, ErrInvalidHash
	}
	if len(salt) > math.MaxUint32 || len(sum) > math.MaxUint32 {
		return parsedArgon2id{}, ErrInvalidHash
	}
	return parsedArgon2id{
		params: Argon2idParams{
			Time:    timeCost,
			Memory:  mem,
			Threads: threads,
			SaltLen: uint32(len(salt)), //nolint:gosec // 上方已校验长度不超过 MaxUint32
			KeyLen:  uint32(len(sum)),  //nolint:gosec // 上方已校验长度不超过 MaxUint32
		},
		salt: salt,
		hash: sum,
	}, nil
}

func verifyArgon2id(encoded string, plain []byte) error {
	if err := checkPassword(plain, MaxPasswordBytes); err != nil {
		if err == ErrPasswordTooLong {
			return ErrMismatch
		}
		return err
	}
	parsed, err := parseArgon2id(encoded)
	if err != nil {
		return err
	}
	sum := argon2.IDKey(plain, parsed.salt, parsed.params.Time, parsed.params.Memory, parsed.params.Threads, parsed.params.KeyLen)
	if subtle.ConstantTimeCompare(sum, parsed.hash) == 1 {
		return nil
	}
	return ErrMismatch
}

func argon2idNeedsRehash(encoded string, want Argon2idParams) bool {
	want, err := want.normalize()
	if err != nil {
		return true
	}
	parsed, err := parseArgon2id(encoded)
	if err != nil {
		return true
	}
	p := parsed.params
	return p.Time != want.Time ||
		p.Memory != want.Memory ||
		p.Threads != want.Threads ||
		p.KeyLen != want.KeyLen ||
		p.SaltLen != want.SaltLen
}
