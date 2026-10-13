package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// 轻量迁移执行器：打印将要执行的 SQL（memory 驱动时仅校验文件存在）。
// 接入真实 DB 时在此打开 database/sql 并按序执行 .up.sql。
func main() {
	dir := flag.String("dir", "migrations", "migrations directory")
	action := flag.String("action", "up", "up|down|status")
	_ = flag.String("config", "", "unused for memory scaffold; reserved for DSN loading")
	flag.Parse()

	entries, err := os.ReadDir(*dir)
	if err != nil {
		log.Fatalf("read migrations: %v", err)
	}
	var ups, downs []string
	for _, e := range entries {
		name := e.Name()
		switch {
		case strings.HasSuffix(name, ".up.sql"):
			ups = append(ups, name)
		case strings.HasSuffix(name, ".down.sql"):
			downs = append(downs, name)
		}
	}
	sort.Strings(ups)
	sort.Strings(downs)

	switch *action {
	case "status":
		fmt.Println("up files:")
		for _, f := range ups {
			fmt.Println(" -", f)
		}
		fmt.Println("down files:")
		for _, f := range downs {
			fmt.Println(" -", f)
		}
	case "up":
		for _, f := range ups {
			path := filepath.Join(*dir, f)
			data, err := os.ReadFile(path) // #nosec G304 -- migration path comes from operator CLI, not request input
			if err != nil {
				log.Fatalf("read %s: %v", path, err)
			}
			fmt.Printf("==> would apply %s (%d bytes)\n", f, len(data))
		}
		fmt.Println("migrate up done (scaffold: SQL not executed; wire DB driver for production)")
	case "down":
		for i := len(downs) - 1; i >= 0; i-- {
			f := downs[i]
			path := filepath.Join(*dir, f)
			data, err := os.ReadFile(path) // #nosec G304 -- migration path comes from operator CLI, not request input
			if err != nil {
				log.Fatalf("read %s: %v", path, err)
			}
			fmt.Printf("==> would rollback %s (%d bytes)\n", f, len(data))
		}
		fmt.Println("migrate down done (scaffold)")
	default:
		log.Fatalf("unknown action: %s", *action)
	}
}
