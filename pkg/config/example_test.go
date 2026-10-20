package config_test

import (
	"fmt"

	"github.com/admin-frame/admin-frame/pkg/config"
)

func ExampleBindEnv() {
	type settings struct {
		Addr string `yaml:"addr"`
		DB   struct {
			DSN string `yaml:"dsn"`
		} `yaml:"db"`
	}
	env := map[string]string{
		"APP_ADDR":   ":9090",
		"APP_DB_DSN": "postgres://localhost/app",
	}
	var cfg settings
	err := config.BindEnv(&cfg, "APP", func(key string) (string, bool) {
		value, ok := env[key]
		return value, ok
	})
	fmt.Println(cfg.Addr, err)
	fmt.Println(cfg.DB.DSN)

	// Output:
	// :9090 <nil>
	// postgres://localhost/app
}
