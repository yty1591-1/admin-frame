package password

// Config 控制 Hasher 的默认算法与参数。
type Config struct {
	// Algorithm 新哈希使用的算法；空则 AlgorithmArgon2id。
	Algorithm Algorithm
	Bcrypt    BcryptParams
	Argon2id  Argon2idParams
}

// Hasher 密码哈希门面：Hash 按配置算法编码，Verify 按前缀自动识别。
type Hasher struct {
	algo   Algorithm
	bcrypt BcryptParams
	argon2 Argon2idParams
}

// New 构造 Hasher；零值 Config 使用 argon2id + OWASP 默认参数。
func New(cfg Config) (*Hasher, error) {
	if cfg.Algorithm == "" {
		cfg.Algorithm = AlgorithmArgon2id
	}
	switch cfg.Algorithm {
	case AlgorithmArgon2id, AlgorithmBcrypt:
	default:
		return nil, ErrUnsupportedAlgorithm
	}
	bcryptParams, err := cfg.Bcrypt.normalize()
	if err != nil {
		return nil, err
	}
	argonParams, err := cfg.Argon2id.normalize()
	if err != nil {
		return nil, err
	}
	return &Hasher{
		algo:   cfg.Algorithm,
		bcrypt: bcryptParams,
		argon2: argonParams,
	}, nil
}

// Must 同 New，失败时 panic（进程启动装配用）。
func Must(cfg Config) *Hasher {
	h, err := New(cfg)
	if err != nil {
		panic(err)
	}
	return h
}

// Hash 使用配置的默认算法哈希明文，返回可持久化的编码串。
func (h *Hasher) Hash(plain []byte) (string, error) {
	if h == nil {
		return "", ErrInvalidConfig
	}
	switch h.algo {
	case AlgorithmBcrypt:
		return hashBcrypt(plain, h.bcrypt)
	default:
		return hashArgon2id(plain, h.argon2)
	}
}

// Verify 校验明文与编码哈希；自动识别 bcrypt / argon2id。
func (h *Hasher) Verify(encoded string, plain []byte) error {
	return Verify(encoded, plain)
}

// NeedsRehash 报告编码哈希是否低于当前 Hasher 配置（算法或成本），
// 供登录成功后透明升级。
func (h *Hasher) NeedsRehash(encoded string) bool {
	if h == nil || encoded == "" {
		return true
	}
	algo, err := DetectAlgorithm(encoded)
	if err != nil {
		return true
	}
	if algo != h.algo {
		return true
	}
	switch algo {
	case AlgorithmBcrypt:
		return bcryptNeedsRehash(encoded, h.bcrypt)
	default:
		return argon2idNeedsRehash(encoded, h.argon2)
	}
}

// Algorithm 返回新哈希使用的算法。
func (h *Hasher) Algorithm() Algorithm {
	if h == nil {
		return ""
	}
	return h.algo
}

// Hash 使用默认 argon2id Hasher 哈希明文。
func Hash(plain []byte) (string, error) {
	return Must(Config{}).Hash(plain)
}

// Verify 按编码前缀自动识别算法并校验（无需 Hasher 实例）。
func Verify(encoded string, plain []byte) error {
	if encoded == "" {
		return ErrInvalidHash
	}
	algo, err := DetectAlgorithm(encoded)
	if err != nil {
		return err
	}
	switch algo {
	case AlgorithmBcrypt:
		return verifyBcrypt(encoded, plain)
	case AlgorithmArgon2id:
		return verifyArgon2id(encoded, plain)
	default:
		return ErrUnsupportedAlgorithm
	}
}
