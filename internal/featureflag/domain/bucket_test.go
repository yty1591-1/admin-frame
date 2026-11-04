package domain

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// TestFlagStatusValid 治理状态取值域。
func TestFlagStatusValid(t *testing.T) {
	for status, want := range map[FlagStatus]bool{
		StatusEnabled:  true,
		StatusDisabled: true,
		StatusExpired:  true,
		"":             false,
		"archived":     false, // 不在治理枚举内：拒绝，避免出现不可解释的状态
	} {
		if got := status.Valid(); got != want {
			t.Fatalf("FlagStatus(%q).Valid() = %v, want %v", status, got, want)
		}
	}
}

// flagFixture 构造一个结构合法的 flag，供 Validate 用例做变异基线。
func flagFixture(t *testing.T) Flag {
	t.Helper()
	flag, err := NewFlag("tenant.invite_email", "租户邀请邮件灰度", nil, true, time.Time{}, "admin-1", nowFixed)
	if err != nil {
		t.Fatalf("NewFlag() error = %v", err)
	}
	return flag
}

// TestFlagValidate 聚合不变量：key 形态、状态、规则上限与逐条校验、
// permanent 与 expires_at 二选一、version 下界。
func TestFlagValidate(t *testing.T) {
	valid := flagFixture(t)
	if err := valid.Validate(); err != nil {
		t.Fatalf("Validate() error = %v", err)
	}
	// 非 permanent 必须带未来 expires_at：同一聚合的另一条合法形态。
	bounded := valid
	bounded.Permanent = false
	bounded.ExpiresAt = nowFixed.Add(24 * time.Hour)
	if err := bounded.Validate(); err != nil {
		t.Fatalf("Validate(bounded) error = %v", err)
	}

	cases := []struct {
		name   string
		mutate func(Flag) Flag
		want   error
	}{
		{"invalid key", func(f Flag) Flag { f.Key = "Tenant.Invite"; return f }, ErrKeyInvalid},
		{"empty key", func(f Flag) Flag { f.Key = ""; return f }, ErrKeyInvalid},
		{"invalid status", func(f Flag) Flag { f.Status = "archived"; return f }, ErrStatusInvalid},
		{"permanent with expiry", func(f Flag) Flag { f.ExpiresAt = nowFixed.Add(time.Hour); return f }, ErrPermanentWithExpiry},
		{"non permanent without expiry", func(f Flag) Flag { f.Permanent = false; return f }, ErrExpiryRequired},
		{"version below 1", func(f Flag) Flag { f.Version = 0; return f }, ErrVersionInvalid},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if err := tc.mutate(valid).Validate(); !errors.Is(err, tc.want) {
				t.Fatalf("Validate() error = %v, want %v", err, tc.want)
			}
		})
	}

	// 规则超上限与单条规则非法：错误需带 rule 下标，便于定位配置项。
	tooMany := valid
	tooMany.Rules = make([]Rule, MaxRules+1)
	if err := tooMany.Validate(); !errors.Is(err, ErrTooManyRules) {
		t.Fatalf("Validate(too many rules) error = %v", err)
	}
	badRule := valid
	badRule.Rules = []Rule{{Channel: "graphql"}}
	if err := badRule.Validate(); err == nil {
		t.Fatal("Validate() must reject invalid rule")
	} else if got := err.Error(); got == "" {
		t.Fatal("rule error must carry a message")
	}
}

// TestFlagMarshalRulesNil 规则为空时序列化为 []（而不是 null）：
// rules_json 列不允许 null，下游 Unmarshal 也按数组解析。
func TestFlagMarshalRulesNil(t *testing.T) {
	flag := Flag{Key: "a.b"}
	raw, err := flag.MarshalRules()
	if err != nil {
		t.Fatalf("MarshalRules() error = %v", err)
	}
	if string(raw) != "[]" {
		t.Fatalf("MarshalRules(nil) = %s, want []", raw)
	}

	flag.Rules = []Rule{{Channel: ChannelAdmin, Percentage: 5000}}
	raw, err = flag.MarshalRules()
	if err != nil {
		t.Fatalf("MarshalRules() error = %v", err)
	}
	var decoded []Rule
	if err := json.Unmarshal(raw, &decoded); err != nil || len(decoded) != 1 {
		t.Fatalf("MarshalRules() = %s (err=%v)", raw, err)
	}
}

// TestBucketStable 同一 (key, subject) 恒返回同序号；不同 key / subject 改变序号。
func TestBucketStable(t *testing.T) {
	const count = 10000
	first := Bucket("tenant.invite_email", "user-1", count)
	if first < 0 || first >= count {
		t.Fatalf("Bucket() = %d, out of range [0,%d)", first, count)
	}
	for i := 0; i < 5; i++ {
		if got := Bucket("tenant.invite_email", "user-1", count); got != first {
			t.Fatalf("Bucket() must be stable: %d vs %d", got, first)
		}
	}
	// 只断言「不恒等」：不同输入在 1/10000 概率上可能碰巧同桶，不算错。
	if Bucket("tenant.invite_email", "user-2", count) == Bucket("tenant.sms", "user-1", count) &&
		Bucket("tenant.invite_email", "user-3", count) == Bucket("tenant.sms", "user-2", count) {
		t.Fatal("different key/subject pairs must not hash identically")
	}
}

// TestBucketThresholdMonotonic 阈值上调只增不减：这是灰度放量的核心性质，
// 一旦破坏，已放量用户会在扩容时被「收回去」。
//
// 判定口径与评估引擎一致：命中 = bucket < percentage（万分比）。
func TestBucketThresholdMonotonic(t *testing.T) {
	const (
		count = 10000
		low   = 2000 // 20%
		high  = 5000 // 50%
	)
	included := func(percentage int, subject string) bool {
		return Bucket("tenant.invite_email", subject, count) < percentage
	}
	var lowHits, highHits int
	for i := 0; i < 500; i++ {
		subject := "user-" + itoa(i)
		inLow, inHigh := included(low, subject), included(high, subject)
		if inLow {
			lowHits++
		}
		if inHigh {
			highHits++
		}
		if inLow && !inHigh {
			t.Fatalf("subject %s: included at %d but excluded at %d", subject, low, high)
		}
	}
	// 防退化：两档都必须真的分出了人，否则上面的包含关系是空断言。
	if lowHits == 0 || highHits == 0 || lowHits >= highHits+1 {
		t.Fatalf("degenerate distribution: low=%d high=%d", lowHits, highHits)
	}
}

// TestBucketUniform 万分比均匀性：分 10 桶，每桶偏差不超过 20%
// （10 万样本下 SHA-256 的理论标准差约 0.3%，20% 是很宽松的回归护栏）。
func TestBucketUniform(t *testing.T) {
	const (
		buckets = 10
		samples = 100000
	)
	var hits [buckets]int
	for i := 0; i < samples; i++ {
		hits[Bucket("tenant.invite_email", "subject-"+itoa(i), buckets)]++
	}
	expected := samples / buckets
	for i, hit := range hits {
		if hit < expected*8/10 || hit > expected*12/10 {
			t.Fatalf("bucket %d hit %d, want within ±20%% of %d", i, hit, expected)
		}
	}
}

// TestBucketDegenerateCount 非正桶数不得 panic（配比 0 时评估引擎仍会调用）。
func TestBucketDegenerateCount(t *testing.T) {
	if got := Bucket("a.b", "u-1", 0); got != 0 {
		t.Fatalf("Bucket(count=0) = %d, want 0", got)
	}
	if got := Bucket("a.b", "u-1", -5); got != 0 {
		t.Fatalf("Bucket(count=-5) = %d, want 0", got)
	}
}

// itoa 避免为测试引入 strconv 依赖（数字到十进制字符串）。
func itoa(v int) string {
	if v == 0 {
		return "0"
	}
	var buf [20]byte
	pos := len(buf)
	for v > 0 {
		pos--
		buf[pos] = byte('0' + v%10)
		v /= 10
	}
	return string(buf[pos:])
}
