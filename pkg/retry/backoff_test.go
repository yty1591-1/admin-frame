package retry

import (
	"testing"
	"time"
)

func TestBackoffDelayCapsAndJitterBounds(t *testing.T) {
	p := Policy{
		InitialBackoff: 10 * time.Millisecond,
		MaxBackoff:     50 * time.Millisecond,
		Multiplier:     10,
		JitterFraction: 0,
	}.Normalize()

	d := p.backoffDelay(3) // 10ms * 100 = 1000ms → cap 50ms
	if d != 50*time.Millisecond {
		t.Fatalf("capped delay=%s", d)
	}

	p.JitterFraction = 1
	for i := 0; i < 50; i++ {
		d = p.backoffDelay(1)
		if d < 0 || d > p.MaxBackoff {
			t.Fatalf("jitter out of range: %s", d)
		}
	}
}
