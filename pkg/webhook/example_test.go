package webhook_test

import (
	"bytes"
	"context"
	"fmt"
	"time"

	"github.com/admin-frame/admin-frame/pkg/clock"
	"github.com/admin-frame/admin-frame/pkg/webhook"
)

func ExampleVerifier() {
	secret := bytes.Repeat([]byte{0x2a}, 32)
	now := time.Unix(1_700_000_000, 0).UTC()
	at := clock.Func(func() time.Time { return now })
	body := []byte(`{"event":"paid"}`)

	signer, err := webhook.NewSigner(webhook.Config{Keys: [][]byte{secret}, Clock: at})
	if err != nil {
		fmt.Println("signer:", err)
		return
	}
	header, err := signer.Sign("msg_1", body)
	if err != nil {
		fmt.Println("sign:", err)
		return
	}

	verifier, err := webhook.NewVerifier(webhook.Config{
		Keys:  [][]byte{secret},
		Clock: at,
		Nonce: webhook.NewMemoryNonceStore(webhook.MemoryNonceStoreConfig{}),
	})
	if err != nil {
		fmt.Println("verifier:", err)
		return
	}
	delivery, err := verifier.Verify(context.Background(), header, body)
	fmt.Println(delivery.ID, err)

	// Output:
	// msg_1 <nil>
}
