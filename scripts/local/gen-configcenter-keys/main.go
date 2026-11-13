// Command gen-configcenter-keys 生成本地 staging 用的快照签名密钥对。
//
// 只服务于本机 / staging 联调：输出可直接 export 的三个环境变量。
// 生产密钥必须由 KMS / Vault / key ceremony 生成，禁止使用本工具产物（SEC-01）。
//
// 用法：
//
//	go run ./scripts/local/gen-configcenter-keys -kid k1
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"flag"
	"fmt"
	"os"
)

func main() {
	kid := flag.String("kid", "k1", "签名密钥版本号（写入快照与公钥配置）")
	flag.Parse()

	seed := make([]byte, ed25519.SeedSize)
	if _, err := rand.Read(seed); err != nil {
		fmt.Fprintf(os.Stderr, "generate seed: %v\n", err)
		os.Exit(1)
	}
	privateKey := ed25519.NewKeyFromSeed(seed)
	publicKey := privateKey.Public().(ed25519.PublicKey)

	fmt.Println("# 本机 / staging 专用；生产必须走 KMS / Vault key ceremony")
	fmt.Printf("export CONFIGCENTER_SNAPSHOT_SIGNING_KEY=%s\n", base64.StdEncoding.EncodeToString(seed))
	fmt.Printf("export CONFIGCENTER_SNAPSHOT_SIGNING_KEY_ID=%s\n", *kid)
	fmt.Printf("export CONFIGCENTER_SNAPSHOT_PUBLIC_KEYS=%s=%s\n", *kid,
		base64.StdEncoding.EncodeToString(publicKey))
}
