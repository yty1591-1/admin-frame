package platform_test

import (
	"strings"
	"testing"
	"time"

	"github.com/admin-frame/admin-frame/internal/platform"
)

func TestLoadConfigAsyncDefaults(t *testing.T) {
	path := writeConfig(t, "admin.yaml", "service: admin-api\nchannel: admin\n")
	cfg, err := platform.LoadConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Async.Enabled || cfg.Async.Outbox.PublishInAPI {
		t.Fatalf("async should be disabled by default: %+v", cfg.Async)
	}
	if cfg.Async.Driver != platform.AsyncDriverDB {
		t.Fatalf("async.driver = %q, want %q", cfg.Async.Driver, platform.AsyncDriverDB)
	}
	if cfg.Async.Outbox.PollInterval != time.Second ||
		cfg.Async.Outbox.BatchSize != 100 ||
		cfg.Async.Outbox.Lease != 30*time.Second ||
		cfg.Async.Outbox.MaxAttempts != 10 ||
		cfg.Async.Outbox.Concurrency != 2 {
		t.Fatalf("async.outbox defaults = %+v", cfg.Async.Outbox)
	}
	if cfg.Async.Consumer.Concurrency != 4 ||
		cfg.Async.Consumer.VisibilityTimeout != time.Minute ||
		cfg.Async.Consumer.RetryBackoff != platform.AsyncBackoffExponential {
		t.Fatalf("async.consumer defaults = %+v", cfg.Async.Consumer)
	}
}

func TestValidateAsyncRules(t *testing.T) {
	baseline := iamBaseConfig()
	if err := baseline.Validate(); err != nil {
		t.Fatalf("async baseline: %v", err)
	}

	cases := []struct {
		name   string
		mutate func(*platform.Config)
		want   string
	}{
		{
			name:   "unsupported driver",
			mutate: func(c *platform.Config) { c.Async.Driver = "kafka" },
			want:   "async.driver",
		},
		{
			name:   "poll interval too small",
			mutate: func(c *platform.Config) { c.Async.Outbox.PollInterval = 10 * time.Millisecond },
			want:   "async.outbox.poll_interval",
		},
		{
			name:   "batch size too large",
			mutate: func(c *platform.Config) { c.Async.Outbox.BatchSize = 501 },
			want:   "async.outbox.batch_size",
		},
		{
			name:   "lease too short",
			mutate: func(c *platform.Config) { c.Async.Outbox.Lease = time.Second },
			want:   "async.outbox.lease",
		},
		{
			name:   "max attempts too large",
			mutate: func(c *platform.Config) { c.Async.Outbox.MaxAttempts = 21 },
			want:   "async.outbox.max_attempts",
		},
		{
			name:   "outbox concurrency too large",
			mutate: func(c *platform.Config) { c.Async.Outbox.Concurrency = 17 },
			want:   "async.outbox.concurrency",
		},
		{
			name:   "consumer concurrency too large",
			mutate: func(c *platform.Config) { c.Async.Consumer.Concurrency = 33 },
			want:   "async.consumer.concurrency",
		},
		{
			name:   "visibility timeout too long",
			mutate: func(c *platform.Config) { c.Async.Consumer.VisibilityTimeout = time.Hour },
			want:   "async.consumer.visibility_timeout",
		},
		{
			name:   "unsupported retry backoff",
			mutate: func(c *platform.Config) { c.Async.Consumer.RetryBackoff = "linear" },
			want:   "async.consumer.retry_backoff",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := iamBaseConfig()
			tc.mutate(cfg)
			if err := cfg.Validate(); err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err=%v want %q", err, tc.want)
			}
		})
	}
}

func TestValidateAsyncRequiresPersistentDB(t *testing.T) {
	memory := iamBaseConfig()
	memory.Async.Enabled = true
	if err := memory.Validate(); err == nil || !strings.Contains(err.Error(), "async.enabled requires a persistent db.driver") {
		t.Fatalf("err=%v want persistent db requirement", err)
	}

	mysql := iamBaseConfig()
	mysql.DB = platform.DBConfig{Driver: "mysql", DSN: "iam:iam@tcp(127.0.0.1:3306)/iam"}
	mysql.Async.Enabled = true
	if err := mysql.Validate(); err != nil {
		t.Fatalf("async with mysql: %v", err)
	}
}

func TestValidateAsyncProductionPublishInAPI(t *testing.T) {
	prodInAPI := prodBaseConfig()
	prodInAPI.Async.Enabled = true
	prodInAPI.Async.Outbox.PublishInAPI = true
	if err := prodInAPI.Validate(); err == nil ||
		!strings.Contains(err.Error(), "async.outbox.publish_in_api must be false") {
		t.Fatalf("err=%v want publish_in_api rejection", err)
	}

	prodWorker := prodBaseConfig()
	prodWorker.Async.Enabled = true
	prodWorker.Async.Outbox.PublishInAPI = false
	if err := prodWorker.Validate(); err != nil {
		t.Fatalf("prod worker config: %v", err)
	}

	localInAPI := iamBaseConfig()
	localInAPI.DB = platform.DBConfig{Driver: "mysql", DSN: "iam:iam@tcp(127.0.0.1:3306)/iam"}
	localInAPI.Async.Enabled = true
	localInAPI.Async.Outbox.PublishInAPI = true
	if err := localInAPI.Validate(); err != nil {
		t.Fatalf("local publish_in_api: %v", err)
	}
}

func TestLoadConfigAsyncEnvOverride(t *testing.T) {
	path := writeConfig(t, "worker.yaml", "service: worker\nchannel: worker\n"+
		"db:\n  driver: mysql\n  dsn: \"iam:iam@tcp(127.0.0.1:3306)/iam\"\n")
	t.Setenv("ADMIN_FRAME_ASYNC_ENABLED", "true")
	t.Setenv("ADMIN_FRAME_ASYNC_OUTBOX_BATCH_SIZE", "50")
	t.Setenv("ADMIN_FRAME_ASYNC_CONSUMER_RETRY_BACKOFF", platform.AsyncBackoffFixed)
	cfg, err := platform.LoadConfig(path)
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if !cfg.Async.Enabled {
		t.Fatal("async.enabled = false, want true from env")
	}
	if cfg.Async.Outbox.BatchSize != 50 {
		t.Fatalf("async.outbox.batch_size = %d, want 50", cfg.Async.Outbox.BatchSize)
	}
	if cfg.Async.Consumer.RetryBackoff != platform.AsyncBackoffFixed {
		t.Fatalf("async.consumer.retry_backoff = %q, want fixed", cfg.Async.Consumer.RetryBackoff)
	}
}
