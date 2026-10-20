package id

import (
	"fmt"
	"strconv"
	"sync"
	"time"
)

// Snowflake 默认位布局（共 63 位有效，最高位恒为 0）：
//
//	| 41 bit 毫秒时间戳 | 10 bit 节点 | 12 bit 序列 |
//
// 约可用 ~69 年（相对自定义纪元）；单节点每毫秒最多 4096 个 ID。
const (
	// DefaultEpoch 默认纪元：2024-01-01T00:00:00Z。
	DefaultEpochMS int64 = 1704067200000

	nodeBits     uint = 10
	sequenceBits uint = 12

	maxNode     int64 = -1 ^ (-1 << nodeBits)     // 1023
	maxSequence int64 = -1 ^ (-1 << sequenceBits) // 4095

	nodeShift      = sequenceBits
	timestampShift = sequenceBits + nodeBits
)

// Options 配置 Snowflake 生成器。
type Options struct {
	// NodeID 机器/进程节点号，范围 [0, 1023]；多实例必须唯一。
	NodeID int64
	// EpochMS 自定义纪元（Unix 毫秒）；≤0 时使用 DefaultEpochMS。
	EpochMS int64
	// Clock 可注入时钟；nil 使用 RealClock。
	Clock Clock
	// MaxBackward 可容忍的时钟回拨；在阈值内自旋等待，超出返回 *ErrClockRollback。
	// 零值默认为 5ms；负值表示不允许任何回拨（立即报错）。
	MaxBackward time.Duration
}

// Snowflake 是并发安全的雪花 ID 生成器。
type Snowflake struct {
	mu sync.Mutex

	nodeID      int64
	epochMS     int64
	clock       Clock
	maxBackward time.Duration

	lastTS   int64
	sequence int64
}

// NewSnowflake 创建生成器；NodeID 非法时返回 *ErrInvalidNode。
func NewSnowflake(opts Options) (*Snowflake, error) {
	if opts.NodeID < 0 || opts.NodeID > maxNode {
		return nil, &ErrInvalidNode{NodeID: opts.NodeID, MaxNode: maxNode}
	}
	epoch := opts.EpochMS
	if epoch <= 0 {
		epoch = DefaultEpochMS
	}
	clock := opts.Clock
	if clock == nil {
		clock = RealClock()
	}
	maxBack := opts.MaxBackward
	if maxBack == 0 {
		maxBack = 5 * time.Millisecond
	}
	return &Snowflake{
		nodeID:      opts.NodeID,
		epochMS:     epoch,
		clock:       clock,
		maxBackward: maxBack,
	}, nil
}

// MustSnowflake 同 NewSnowflake，失败时 panic。
func MustSnowflake(opts Options) *Snowflake {
	s, err := NewSnowflake(opts)
	if err != nil {
		panic(err)
	}
	return s
}

// Next 生成下一个 int64 ID。
func (s *Snowflake) Next() (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.currentMS()
	if now < s.lastTS {
		back := time.Duration(s.lastTS-now) * time.Millisecond
		if s.maxBackward < 0 || back > s.maxBackward {
			return 0, &ErrClockRollback{Backward: back}
		}
		// 小幅回拨：等待追上 lastTS
		for now < s.lastTS {
			now = s.currentMS()
		}
	}

	if now == s.lastTS {
		s.sequence = (s.sequence + 1) & maxSequence
		if s.sequence == 0 {
			now = s.waitNextMS(s.lastTS)
		}
	} else {
		s.sequence = 0
	}
	s.lastTS = now

	ts := now - s.epochMS
	if ts < 0 {
		return 0, fmt.Errorf("id: current time before epoch")
	}
	if ts >= (1 << 41) {
		return 0, ErrOvertime
	}

	id := (ts << timestampShift) | (s.nodeID << nodeShift) | s.sequence
	return id, nil
}

// NextString 返回十进制字符串 ID。
func (s *Snowflake) NextString() (string, error) {
	n, err := s.Next()
	if err != nil {
		return "", err
	}
	return strconv.FormatInt(n, 10), nil
}

// NodeID 返回配置的节点号。
func (s *Snowflake) NodeID() int64 { return s.nodeID }

// EpochMS 返回纪元（Unix 毫秒）。
func (s *Snowflake) EpochMS() int64 { return s.epochMS }

func (s *Snowflake) currentMS() int64 {
	return s.clock.Now().UTC().UnixMilli()
}

func (s *Snowflake) waitNextMS(last int64) int64 {
	now := s.currentMS()
	for now <= last {
		now = s.currentMS()
	}
	return now
}

// Parts 是解析后的 Snowflake 组成。
type Parts struct {
	// Time 为 ID 对应的 UTC 时间（毫秒精度）。
	Time time.Time
	// TimestampMS 相对纪元的毫秒数。
	TimestampMS int64
	// NodeID 节点号。
	NodeID int64
	// Sequence 序列号。
	Sequence int64
}

// Parse 按默认位布局与给定纪元拆解 ID。
// epochMS ≤0 时使用 DefaultEpochMS。
func Parse(id, epochMS int64) Parts {
	if epochMS <= 0 {
		epochMS = DefaultEpochMS
	}
	seq := id & maxSequence
	node := (id >> nodeShift) & maxNode
	ts := id >> timestampShift
	return Parts{
		Time:        time.UnixMilli(epochMS + ts).UTC(),
		TimestampMS: ts,
		NodeID:      node,
		Sequence:    seq,
	}
}

// ParseString 解析十进制字符串 ID。
func ParseString(s string, epochMS int64) (Parts, error) {
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return Parts{}, fmt.Errorf("id: parse %q: %w", s, err)
	}
	return Parse(n, epochMS), nil
}
