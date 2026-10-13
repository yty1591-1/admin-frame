package apperr_test

import (
	stderrors "errors"
	"testing"

	"github.com/admin-frame/admin-frame/internal/shared/apperr"
	xerr "github.com/admin-frame/admin-frame/pkg/errors"
)

func TestAppErrorIsSentinel(t *testing.T) {
	err := apperr.NotFound("order missing")
	if !stderrors.Is(err, apperr.ErrNotFound) {
		t.Fatal("expected Is ErrNotFound")
	}
	if !stderrors.Is(err, xerr.ErrNotFound) {
		t.Fatal("expected Is xerr.ErrNotFound")
	}
	if !xerr.IsCode(err, xerr.CodeNotFound) {
		t.Fatal("expected IsCode")
	}
	if err.Code != apperr.CodeNotFound {
		t.Fatalf("biz code=%d", err.Code)
	}
	if err.SemanticCode() != xerr.CodeNotFound {
		t.Fatalf("semantic=%s", err.SemanticCode())
	}
}

func TestInternalWrapsCause(t *testing.T) {
	root := stderrors.New("db down")
	err := apperr.Internal("save order", root)
	if !stderrors.Is(err, root) {
		t.Fatal("cause not linked")
	}
	if err.Code != apperr.CodeInternal {
		t.Fatalf("code=%d", err.Code)
	}
}

func TestRawSentinel(t *testing.T) {
	err := apperr.ErrUnauthorized
	if !stderrors.Is(err, xerr.ErrUnauthenticated) {
		t.Fatal("sentinel mismatch")
	}
	if xerr.HTTPStatusOf(err) != 401 {
		t.Fatalf("http=%d", xerr.HTTPStatusOf(err))
	}
}

func TestTooManyRequests(t *testing.T) {
	err := apperr.TooManyRequests("too many requests")
	if err.Code != apperr.CodeTooManyRequests {
		t.Fatalf("code=%d", err.Code)
	}
	if err.SemanticCode() != xerr.CodeResourceExhausted {
		t.Fatalf("semantic=%s", err.SemanticCode())
	}
	if !xerr.IsCode(err, xerr.CodeResourceExhausted) {
		t.Fatal("expected IsCode resource_exhausted")
	}
}
