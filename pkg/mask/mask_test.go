package mask_test

import (
	"strings"
	"testing"

	"github.com/admin-frame/admin-frame/pkg/mask"
)

func TestPhone(t *testing.T) {
	if got := mask.Phone("13812345678"); got != "138****5678" {
		t.Fatalf("got=%s", got)
	}
	if got := mask.Phone("138-1234-5678"); got != "138****5678" {
		t.Fatalf("got=%s", got)
	}
}

func TestIDCard(t *testing.T) {
	id := "110101199001011234"
	got := mask.IDCard(id)
	if !strings.HasPrefix(got, "1101") || !strings.HasSuffix(got, "1234") || strings.Contains(got, "1990") {
		t.Fatalf("got=%s", got)
	}
	id18x := "11010119900101123X"
	got = mask.IDCard(id18x)
	if !strings.HasSuffix(got, "123X") && !strings.HasSuffix(got, "23X") {
		// Keep(4,4) → last 4 of 18 runes
		if got[len(got)-4:] != "123X" {
			t.Fatalf("got=%s", got)
		}
	}
}

func TestBankCard(t *testing.T) {
	if got := mask.BankCard("6222021234567890123"); got != "****0123" {
		t.Fatalf("got=%s", got)
	}
}

func TestEmail(t *testing.T) {
	if got := mask.Email("alice@example.com"); got != "a***@example.com" {
		t.Fatalf("got=%s", got)
	}
	if got := mask.Email("a@b.co"); got != "a***@b.co" {
		t.Fatalf("got=%s", got)
	}
}

func TestName(t *testing.T) {
	if got := mask.Name("张"); got != "*" {
		t.Fatalf("got=%s", got)
	}
	if got := mask.Name("张三"); got != "张*" {
		t.Fatalf("got=%s", got)
	}
	if got := mask.Name("欧阳锋"); got != "欧*锋" {
		t.Fatalf("got=%s", got)
	}
}

func TestTokenAndAuthorization(t *testing.T) {
	tok := "abcdefghijklmnop"
	got := mask.Token(tok)
	if !strings.HasPrefix(got, "abcd") || !strings.HasSuffix(got, "mnop") {
		t.Fatalf("got=%s", got)
	}
	auth := mask.Authorization("Bearer " + tok)
	if !strings.HasPrefix(auth, "Bearer ") || strings.Contains(auth, "efghijkl") {
		t.Fatalf("got=%s", auth)
	}
}

func TestKeepAndAll(t *testing.T) {
	if got := mask.Keep("abcdef", 2, 2); got != "ab**ef" {
		t.Fatalf("got=%s", got)
	}
	if got := mask.Keep("ab", 1, 1); got != "**" {
		t.Fatalf("short keep got=%s", got)
	}
	if got := mask.All("hi"); got != "**" {
		t.Fatalf("got=%s", got)
	}
}

func TestRedactSecrets(t *testing.T) {
	in := `password=s3cret Bearer eyJhbGciOiJIUzI1NiJ9.abc api_key=xyz`
	out := mask.RedactSecrets(in)
	if strings.Contains(out, "s3cret") || strings.Contains(out, "eyJhbGci") || strings.Contains(out, "xyz") {
		t.Fatalf("not redacted: %s", out)
	}
	if !strings.Contains(out, "password=***") {
		t.Fatalf("kv: %s", out)
	}
}

func TestText(t *testing.T) {
	in := "user 13812345678 id 110101199001011234 mail bob@example.com"
	out := mask.Text(in)
	if strings.Contains(out, "13812345678") || strings.Contains(out, "19900101") || strings.Contains(out, "bob@") {
		t.Fatalf("out=%s", out)
	}
	if !strings.Contains(out, "138****5678") || !strings.Contains(out, "b***@example.com") {
		t.Fatalf("out=%s", out)
	}
}

func TestMapAndField(t *testing.T) {
	in := map[string]string{
		"phone":    "13812345678",
		"email":    "alice@example.com",
		"password": "secret",
		"note":     "call 13900001111",
		"filename": "report.pdf",
	}
	out := mask.Map(in)
	if out["phone"] != "138****5678" {
		t.Fatalf("phone=%s", out["phone"])
	}
	if out["email"] != "a***@example.com" {
		t.Fatalf("email=%s", out["email"])
	}
	if out["password"] != "***" {
		t.Fatalf("password=%s", out["password"])
	}
	if strings.Contains(out["note"], "13900001111") {
		t.Fatalf("note=%s", out["note"])
	}
	if out["filename"] != "report.pdf" {
		t.Fatalf("filename should not use Name mask: %s", out["filename"])
	}
	if in["password"] != "secret" {
		t.Fatal("input map must not be mutated")
	}
	if got := mask.Field("user_phone", "13812345678"); got != "138****5678" {
		t.Fatalf("fuzzy=%s", got)
	}
}
