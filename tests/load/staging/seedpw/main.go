// 演练夹具：用生产同款编码器(Argon2id)为压测账号生成口令凭据 SQL。
//
// SCIM 建号按设计不落本地口令（纯 SSO），而登录 SLO 必须覆盖口令校验链路，
// 因此压测账号的口令由本夹具注入；账号本身仍由 POST /admin/scim/v2/Users 生产路径创建。
//
// 用法：go run ./tmp/stg-eq/hashpw -password 'Load#Staging2026' -ids id1,id2,... > creds.sql
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/admin-frame/admin-frame/pkg/password"
)

func main() {
	plain := flag.String("password", "", "明文口令（仅写库哈希，不落盘）")
	ids := flag.String("ids", "", "账号 ID，逗号分隔")
	idsFile := flag.String("ids-file", "", "账号 ID 文件（每行一个）；命令行过长时使用")
	flag.Parse()
	if *plain == "" || (*ids == "" && *idsFile == "") {
		fmt.Fprintln(os.Stderr, "password and ids/ids-file are required")
		os.Exit(2)
	}
	if *idsFile != "" {
		raw, err := os.ReadFile(*idsFile)
		if err != nil {
			fmt.Fprintln(os.Stderr, "read ids-file:", err)
			os.Exit(1)
		}
		*ids = strings.Join(strings.Fields(string(raw)), ",")
	}
	encoded, err := password.Hash([]byte(*plain))
	if err != nil {
		fmt.Fprintln(os.Stderr, "hash:", err)
		os.Exit(1)
	}
	now := time.Now().UTC().Format("2006-01-02 15:04:05.000")
	list := strings.Split(*ids, ",")
	fmt.Println("START TRANSACTION;")
	for _, id := range list {
		acct := strings.TrimSpace(id)
		// 凭据主键从账号 ID 派生（同一账号幂等），避开雪花发号与既有号段。
		credID := int64(0)
		fmt.Sscanf(acct, "%d", &credID)
		credID += 8000000000000000000
		// 只在缺口令凭据时插入：SCIM 账号首次注入，重跑不覆盖用户自助改过的口令。
		fmt.Printf("INSERT INTO identity_credential (id, account_id, credential_type, credential_hash, algorithm, password_history, created_at, updated_at) "+
			"SELECT %d, %s, 'password', '%s', 'argon2id', NULL, '%s', '%s' FROM DUAL "+
			"WHERE NOT EXISTS (SELECT 1 FROM identity_credential WHERE account_id = %s AND credential_type = 'password');\n",
			credID, acct, encoded, now, now, acct)
	}
	fmt.Println("COMMIT;")
}
