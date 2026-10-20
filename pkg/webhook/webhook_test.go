package webhook_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	pkgerrors "github.com/admin-frame/admin-frame/pkg/errors"
	"github.com/admin-frame/admin-frame/pkg/webhook"
)

func TestSignVerifyRoundTrip(t *testing.T) {
	algorithms := []struct {
		name      string
		algorithm webhook.Algorithm
	}{
		{name: "hmac-sha256", algorithm: webhook.HMACSHA256()},
		{name: "hmac-sha512", algorithm: webhook.HMACSHA512()},
	}
	body := []byte(`{"event":"paid","order_id":"o-1"}`)
	for _, tt := range algorithms {
		t.Run(tt.name, func(t *testing.T) {
			signerCfg := testSignerConfig()
			signerCfg.Algorithm = tt.algorithm
			verifierCfg := testVerifierConfig()
			verifierCfg.Algorithm = tt.algorithm

			header := signAt(t, mustSigner(t, signerCfg), "msg_roundtrip", body, signingTime)
			delivery, err := mustVerifier(t, verifierCfg).Verify(t.Context(), header, body)
			if err != nil {
				t.Fatalf("verify: %v", err)
			}
			if delivery.ID != "msg_roundtrip" {
				t.Fatalf("id=%q", delivery.ID)
			}
			if !delivery.Timestamp.Equal(signingTime) {
				t.Fatalf("timestamp=%s want %s", delivery.Timestamp, signingTime)
			}
			if delivery.Timestamp.Location() != time.UTC {
				t.Fatalf("timestamp location=%s want UTC", delivery.Timestamp.Location())
			}
			if !bytes.Equal(delivery.Body, body) {
				t.Fatalf("body=%q", delivery.Body)
			}
		})
	}
}

func TestVerifyRejectsTampering(t *testing.T) {
	signer := mustSigner(t, testSignerConfig())
	verifier := mustVerifier(t, testVerifierConfig())
	body := []byte(`{"event":"paid"}`)
	signed := signAt(t, signer, "msg_tamper", body, signingTime)

	tests := []struct {
		name   string
		mutate func(http.Header, []byte) []byte
	}{
		{name: "body changed", mutate: func(_ http.Header, raw []byte) []byte {
			return append(append([]byte(nil), raw...), ' ')
		}},
		{name: "id changed", mutate: func(header http.Header, raw []byte) []byte {
			header.Set(webhook.HeaderID, "msg_other")
			return raw
		}},
		{name: "timestamp changed", mutate: func(header http.Header, raw []byte) []byte {
			header.Set(webhook.HeaderTimestamp, strconv.FormatInt(signingTime.Unix()+1, 10))
			return raw
		}},
		{name: "signature changed", mutate: func(header http.Header, raw []byte) []byte {
			header.Set(webhook.HeaderSignature, "v1,"+base64.StdEncoding.EncodeToString(make([]byte, 32)))
			return raw
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			header := signed.Clone()
			tampered := tt.mutate(header, body)
			if _, err := verifier.Verify(t.Context(), header, tampered); !errors.Is(err, webhook.ErrInvalidSignature) {
				t.Fatalf("err=%v want ErrInvalidSignature", err)
			}
		})
	}

	// 失败路径不得消费 nonce，否则合法重试会被误判为重放。
	if _, err := verifier.Verify(t.Context(), signed, body); err != nil {
		t.Fatalf("valid delivery after tampered attempts: %v", err)
	}
	if _, err := verifier.Verify(t.Context(), signed, body); !errors.Is(err, webhook.ErrReplay) {
		t.Fatalf("err=%v want ErrReplay", err)
	}
}

func TestVerifyTimestampWindow(t *testing.T) {
	const tolerance = 5 * time.Minute
	tests := []struct {
		name   string
		offset time.Duration
		want   error
	}{
		{name: "expired", offset: tolerance + time.Second, want: webhook.ErrTimestampExpired},
		{name: "future", offset: -tolerance - time.Second, want: webhook.ErrTimestampFuture},
		{name: "edge past accepted", offset: tolerance},
		{name: "edge future accepted", offset: -tolerance},
	}
	signer := mustSigner(t, testSignerConfig())
	body := []byte(`{"event":"paid"}`)
	for i, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := testVerifierConfig()
			cfg.Tolerance = tolerance
			cfg.Clock = fixedClock(signingTime.Add(tt.offset))
			header := signAt(t, signer, fmt.Sprintf("msg_window_%d", i), body, signingTime)

			_, err := mustVerifier(t, cfg).Verify(t.Context(), header, body)
			if tt.want == nil {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}
				return
			}
			if !errors.Is(err, tt.want) {
				t.Fatalf("err=%v want %v", err, tt.want)
			}
		})
	}
}

func TestVerifyReplay(t *testing.T) {
	verifier := mustVerifier(t, testVerifierConfig())
	signer := mustSigner(t, testSignerConfig())
	body := []byte(`{"event":"paid"}`)
	header := signAt(t, signer, "msg_replay", body, signingTime)

	if _, err := verifier.Verify(t.Context(), header, body); err != nil {
		t.Fatalf("first delivery: %v", err)
	}
	_, err := verifier.Verify(t.Context(), header, body)
	if !errors.Is(err, webhook.ErrReplay) {
		t.Fatalf("err=%v want ErrReplay", err)
	}
	if !pkgerrors.IsCode(err, pkgerrors.CodeAlreadyExists) {
		t.Fatalf("code=%s want already_exists", pkgerrors.CodeOf(err))
	}

	other := signAt(t, signer, "msg_replay_other", body, signingTime)
	if _, err := verifier.Verify(t.Context(), other, body); err != nil {
		t.Fatalf("distinct id should be accepted: %v", err)
	}
}

func TestVerifyKeyRotation(t *testing.T) {
	keys := [][]byte{testKey(0x01), testKey(0x02)}
	signerCfg := testSignerConfig()
	signerCfg.Keys = keys
	signer := mustSigner(t, signerCfg)
	body := []byte(`{"event":"paid"}`)
	header := signAt(t, signer, "msg_rotation", body, signingTime)

	if got := len(strings.Fields(header.Get(webhook.HeaderSignature))); got != len(keys) {
		t.Fatalf("signature count=%d want %d", got, len(keys))
	}
	for _, key := range keys {
		cfg := testVerifierConfig()
		cfg.Keys = [][]byte{key}
		if _, err := mustVerifier(t, cfg).Verify(t.Context(), header, body); err != nil {
			t.Fatalf("verify with rotated key: %v", err)
		}
	}

	cfg := testVerifierConfig()
	cfg.Keys = [][]byte{testKey(0x03)}
	if _, err := mustVerifier(t, cfg).Verify(t.Context(), header, body); !errors.Is(err, webhook.ErrInvalidSignature) {
		t.Fatalf("unknown key err=%v want ErrInvalidSignature", err)
	}
}

func TestVerifySignatureVersion(t *testing.T) {
	body := []byte(`{"event":"paid"}`)
	signerCfg := testSignerConfig()
	signerCfg.Version = "v2"
	signer := mustSigner(t, signerCfg)

	verifierCfg := testVerifierConfig()
	verifierCfg.Version = "v2"
	if _, err := mustVerifier(t, verifierCfg).Verify(t.Context(), signAt(t, signer, "msg_v2", body, signingTime), body); err != nil {
		t.Fatalf("verify v2: %v", err)
	}

	v1Verifier := mustVerifier(t, testVerifierConfig())
	_, err := v1Verifier.Verify(t.Context(), signAt(t, signer, "msg_v2_mismatch", body, signingTime), body)
	if !errors.Is(err, webhook.ErrUnsupportedVersion) {
		t.Fatalf("err=%v want ErrUnsupportedVersion", err)
	}

	// 发送方灰度期间可同时携带多个版本；接收方只取自己支持的版本。
	v1SignerCfg := testSignerConfig()
	v1Signer := mustSigner(t, v1SignerCfg)
	v1Header := signAt(t, v1Signer, "msg_multi_version", body, signingTime)
	v2Header := signAt(t, signer, "msg_multi_version", body, signingTime)
	combined := v1Header.Clone()
	combined.Set(webhook.HeaderSignature, v1Header.Get(webhook.HeaderSignature)+" "+v2Header.Get(webhook.HeaderSignature))
	if _, err := v1Verifier.Verify(t.Context(), combined, body); err != nil {
		t.Fatalf("verify combined versions: %v", err)
	}
}

func TestVerifyHeaderValidation(t *testing.T) {
	signer := mustSigner(t, testSignerConfig())
	body := []byte(`{"event":"paid"}`)
	signed := signAt(t, signer, "msg_header", body, signingTime)

	tests := []struct {
		name   string
		mutate func(http.Header)
		want   error
	}{
		{name: "missing id", mutate: func(h http.Header) { h.Del(webhook.HeaderID) }, want: webhook.ErrMissingHeader},
		{name: "missing timestamp", mutate: func(h http.Header) { h.Del(webhook.HeaderTimestamp) }, want: webhook.ErrMissingHeader},
		{name: "missing signature", mutate: func(h http.Header) { h.Del(webhook.HeaderSignature) }, want: webhook.ErrMissingHeader},
		{name: "id too long", mutate: func(h http.Header) { h.Set(webhook.HeaderID, strings.Repeat("a", 201)) }, want: webhook.ErrMalformedHeader},
		{name: "id with space", mutate: func(h http.Header) { h.Set(webhook.HeaderID, "msg header") }, want: webhook.ErrMalformedHeader},
		{name: "id with control char", mutate: func(h http.Header) { h.Set(webhook.HeaderID, "msg\x01") }, want: webhook.ErrMalformedHeader},
		{name: "timestamp not numeric", mutate: func(h http.Header) { h.Set(webhook.HeaderTimestamp, "abc") }, want: webhook.ErrMalformedHeader},
		{name: "timestamp leading zero", mutate: func(h http.Header) { h.Set(webhook.HeaderTimestamp, "001700000000") }, want: webhook.ErrMalformedHeader},
		{name: "timestamp negative", mutate: func(h http.Header) { h.Set(webhook.HeaderTimestamp, "-1") }, want: webhook.ErrMalformedHeader},
		{name: "timestamp overflow", mutate: func(h http.Header) {
			h.Set(webhook.HeaderTimestamp, "17000000000000000000")
		}, want: webhook.ErrMalformedHeader},
		{name: "signature without separator", mutate: func(h http.Header) { h.Set(webhook.HeaderSignature, "nocomma") }, want: webhook.ErrMalformedHeader},
		{name: "signature without value", mutate: func(h http.Header) { h.Set(webhook.HeaderSignature, "v1,") }, want: webhook.ErrMalformedHeader},
		{name: "signature invalid base64", mutate: func(h http.Header) { h.Set(webhook.HeaderSignature, "v1,!!!!") }, want: webhook.ErrMalformedHeader},
		{name: "signature unknown version", mutate: func(h http.Header) { h.Set(webhook.HeaderSignature, "v9,AAAA") }, want: webhook.ErrUnsupportedVersion},
		{name: "signature too many entries", mutate: func(h http.Header) {
			h.Set(webhook.HeaderSignature, strings.Repeat("v1,AAAA ", 9))
		}, want: webhook.ErrMalformedHeader},
		{name: "signature too large", mutate: func(h http.Header) {
			h.Set(webhook.HeaderSignature, "v1,"+strings.Repeat("A", 9000))
		}, want: webhook.ErrMalformedHeader},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			header := signed.Clone()
			tt.mutate(header)
			_, err := mustVerifier(t, testVerifierConfig()).Verify(t.Context(), header, body)
			if !errors.Is(err, tt.want) {
				t.Fatalf("err=%v want %v", err, tt.want)
			}
		})
	}
}

func TestVerifyUnpaddedSignature(t *testing.T) {
	body := []byte(`{"event":"paid"}`)
	header := signAt(t, mustSigner(t, testSignerConfig()), "msg_padding", body, signingTime)
	raw := header.Get(webhook.HeaderSignature)
	header.Set(webhook.HeaderSignature, strings.TrimRight(raw, "="))
	if _, err := mustVerifier(t, testVerifierConfig()).Verify(t.Context(), header, body); err != nil {
		t.Fatalf("verify unpadded signature: %v", err)
	}
}

func TestVerifyBodyLimit(t *testing.T) {
	signerCfg := testSignerConfig()
	signerCfg.MaxBodyBytes = 64
	body := bytes.Repeat([]byte("x"), 17)
	header := signAt(t, mustSigner(t, signerCfg), "msg_limit", body, signingTime)

	verifierCfg := testVerifierConfig()
	verifierCfg.MaxBodyBytes = 16
	_, err := mustVerifier(t, verifierCfg).Verify(t.Context(), header, body)
	if !errors.Is(err, webhook.ErrBodyTooLarge) {
		t.Fatalf("err=%v want ErrBodyTooLarge", err)
	}
}

func TestVerifyRequestRestoresBody(t *testing.T) {
	body := []byte(`{"event":"paid"}`)
	verifier := mustVerifier(t, testVerifierConfig())
	header := signAt(t, mustSigner(t, testSignerConfig()), "msg_request", body, signingTime)

	req := httptest.NewRequest(http.MethodPost, "/webhooks/pay", bytes.NewReader(body))
	for name, values := range header {
		req.Header[name] = append([]string(nil), values...)
	}
	delivery, err := verifier.VerifyRequest(t.Context(), req)
	if err != nil {
		t.Fatalf("verify request: %v", err)
	}
	if delivery.ID != "msg_request" {
		t.Fatalf("id=%q", delivery.ID)
	}
	again, err := io.ReadAll(req.Body)
	if err != nil {
		t.Fatalf("read restored body: %v", err)
	}
	if !bytes.Equal(again, body) {
		t.Fatalf("restored body=%q", again)
	}
}

func TestVerifyRequestRejectsNil(t *testing.T) {
	verifier := mustVerifier(t, testVerifierConfig())
	if _, err := verifier.VerifyRequest(t.Context(), nil); !errors.Is(err, webhook.ErrInvalidConfig) {
		t.Fatalf("nil request err=%v", err)
	}
	var nilVerifier *webhook.Verifier
	if _, err := nilVerifier.Verify(t.Context(), nil, nil); !errors.Is(err, webhook.ErrInvalidConfig) {
		t.Fatalf("nil verifier err=%v", err)
	}
	if _, err := nilVerifier.VerifyRequest(t.Context(), httptest.NewRequest(http.MethodPost, "/", http.NoBody)); !errors.Is(err, webhook.ErrInvalidConfig) {
		t.Fatalf("nil verifier request err=%v", err)
	}
}

func TestSignerErrors(t *testing.T) {
	cfg := testSignerConfig()
	signer := mustSigner(t, cfg)
	body := []byte(`{"event":"paid"}`)

	tests := []struct {
		name string
		sign func() error
		want error
	}{
		{name: "empty id", sign: func() error {
			_, err := signer.SignAt("", body, signingTime)
			return err
		}, want: webhook.ErrMissingHeader},
		{name: "id too long", sign: func() error {
			_, err := signer.SignAt(strings.Repeat("a", 201), body, signingTime)
			return err
		}, want: webhook.ErrMalformedHeader},
		{name: "id with space", sign: func() error {
			_, err := signer.SignAt("msg 1", body, signingTime)
			return err
		}, want: webhook.ErrMalformedHeader},
		{name: "zero time", sign: func() error {
			_, err := signer.SignAt("msg_zero", body, time.Time{})
			return err
		}, want: webhook.ErrInvalidConfig},
		{name: "before epoch", sign: func() error {
			_, err := signer.SignAt("msg_negative", body, time.Unix(-1, 0))
			return err
		}, want: webhook.ErrInvalidConfig},
		{name: "nil request", sign: func() error {
			return signer.SignRequest(nil, "msg_nil", body)
		}, want: webhook.ErrInvalidConfig},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := tt.sign(); !errors.Is(err, tt.want) {
				t.Fatalf("err=%v want %v", err, tt.want)
			}
		})
	}

	var nilSigner *webhook.Signer
	if _, err := nilSigner.Sign("msg_nil", body); !errors.Is(err, webhook.ErrInvalidConfig) {
		t.Fatalf("nil signer err=%v", err)
	}
	if _, err := nilSigner.SignAt("msg_nil", body, signingTime); !errors.Is(err, webhook.ErrInvalidConfig) {
		t.Fatalf("nil signer at err=%v", err)
	}
}

func TestSignerBodyLimit(t *testing.T) {
	cfg := testSignerConfig()
	cfg.MaxBodyBytes = 8
	_, err := mustSigner(t, cfg).SignAt("msg_big", bytes.Repeat([]byte("x"), 9), signingTime)
	if !errors.Is(err, webhook.ErrBodyTooLarge) {
		t.Fatalf("err=%v want ErrBodyTooLarge", err)
	}
}

func TestSignRequest(t *testing.T) {
	body := []byte(`{"event":"paid"}`)
	signer := mustSigner(t, testSignerConfig())
	req := httptest.NewRequest(http.MethodPost, "https://example.com/hook", bytes.NewReader(body))
	if err := signer.SignRequest(req, "msg_sign_request", body); err != nil {
		t.Fatalf("sign request: %v", err)
	}
	if req.Header.Get(webhook.HeaderID) != "msg_sign_request" {
		t.Fatalf("id header=%q", req.Header.Get(webhook.HeaderID))
	}
	if _, err := mustVerifier(t, testVerifierConfig()).Verify(t.Context(), req.Header, body); err != nil {
		t.Fatalf("verify signed request: %v", err)
	}
}

func TestDeliveryClone(t *testing.T) {
	body := []byte("payload")
	delivery := webhook.Delivery{ID: "msg_clone", Timestamp: signingTime, Body: body}
	clone := delivery.Clone()
	clone.Body[0] = 'P'
	if delivery.Body[0] != 'p' {
		t.Fatalf("clone mutated original: %q", delivery.Body)
	}
}

func TestDeliveryContext(t *testing.T) {
	if _, ok := webhook.DeliveryFrom(t.Context()); ok {
		t.Fatal("empty context should not contain delivery")
	}
	delivery := webhook.Delivery{ID: "msg_ctx"}
	ctx := webhook.WithDelivery(t.Context(), delivery)
	got, ok := webhook.DeliveryFrom(ctx)
	if !ok || got.ID != delivery.ID {
		t.Fatalf("delivery=%+v ok=%v", got, ok)
	}
}

// recordingObserver 保存事件快照，便于断言字段与顺序。
type recordingObserver struct {
	mu     sync.Mutex
	events []webhook.Event
}

func (o *recordingObserver) Observe(_ context.Context, ev webhook.Event) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.events = append(o.events, ev)
}

func (o *recordingObserver) snapshot() []webhook.Event {
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]webhook.Event, len(o.events))
	copy(out, o.events)
	return out
}

func TestObserverEvents(t *testing.T) {
	observer := &recordingObserver{}
	cfg := testVerifierConfig()
	cfg.Observer = observer
	verifier := mustVerifier(t, cfg)
	signer := mustSigner(t, testSignerConfig())
	body := []byte(`{"event":"paid"}`)
	header := signAt(t, signer, "msg_observer", body, signingTime)

	if _, err := verifier.Verify(t.Context(), header, append(body, ' ')); !errors.Is(err, webhook.ErrInvalidSignature) {
		t.Fatalf("tampered verify err=%v", err)
	}
	if _, err := verifier.Verify(t.Context(), header, body); err != nil {
		t.Fatalf("first verify: %v", err)
	}
	if _, err := verifier.Verify(t.Context(), header, body); !errors.Is(err, webhook.ErrReplay) {
		t.Fatalf("replay verify err=%v", err)
	}

	events := observer.snapshot()
	if len(events) != 3 {
		t.Fatalf("events=%d want 3", len(events))
	}
	for _, ev := range events {
		if ev.Channel != "webhook" || ev.Operation != "verify" || ev.Version != webhook.DefaultVersion {
			t.Fatalf("unexpected envelope: %+v", ev)
		}
	}
	if events[0].Result != "failure" || events[0].Code != "unauthenticated" || events[0].Replay {
		t.Fatalf("failure event=%+v", events[0])
	}
	if events[1].Result != "success" || events[1].Code != "" || events[1].Replay {
		t.Fatalf("success event=%+v", events[1])
	}
	if events[2].Result != "failure" || events[2].Code != "already_exists" || !events[2].Replay {
		t.Fatalf("replay event=%+v", events[2])
	}
}

type failingNonce struct {
	err error
}

func (f failingNonce) Claim(context.Context, string, time.Duration) (bool, error) {
	return false, f.err
}

func TestVerifyNonceFailure(t *testing.T) {
	signer := mustSigner(t, testSignerConfig())
	body := []byte(`{"event":"paid"}`)
	header := signAt(t, signer, "msg_nonce_fail", body, signingTime)

	cfg := testVerifierConfig()
	cfg.Nonce = failingNonce{err: errors.New("redis unavailable")}
	_, err := mustVerifier(t, cfg).Verify(t.Context(), header, body)
	if !errors.Is(err, webhook.ErrNonceUnavailable) {
		t.Fatalf("err=%v want ErrNonceUnavailable", err)
	}
	if !pkgerrors.IsCode(err, pkgerrors.CodeUnavailable) {
		t.Fatalf("code=%s want unavailable", pkgerrors.CodeOf(err))
	}

	cfg = testVerifierConfig()
	cfg.Nonce = failingNonce{err: context.Canceled}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = mustVerifier(t, cfg).Verify(ctx, header, body)
	if !errors.Is(err, webhook.ErrNonceUnavailable) {
		t.Fatalf("canceled err=%v want ErrNonceUnavailable", err)
	}
	if !pkgerrors.IsCode(err, pkgerrors.CodeCanceled) {
		t.Fatalf("canceled code=%s want canceled", pkgerrors.CodeOf(err))
	}
}

func BenchmarkVerifierVerify(b *testing.B) {
	cfg := webhook.Config{
		Keys:  [][]byte{testKey(0x2a)},
		Clock: fixedClock(signingTime),
		Nonce: alwaysFirstNonce{},
	}
	verifier, err := webhook.NewVerifier(cfg)
	if err != nil {
		b.Fatalf("new verifier: %v", err)
	}
	signer, err := webhook.NewSigner(cfg)
	if err != nil {
		b.Fatalf("new signer: %v", err)
	}
	body := bytes.Repeat([]byte("x"), 1024)
	header, err := signer.SignAt("msg_bench", body, signingTime)
	if err != nil {
		b.Fatalf("sign: %v", err)
	}
	ctx := context.Background()
	b.ReportAllocs()
	b.SetBytes(int64(len(body)))
	for b.Loop() {
		if _, verifyErr := verifier.Verify(ctx, header, body); verifyErr != nil {
			b.Fatalf("verify: %v", verifyErr)
		}
	}
}
