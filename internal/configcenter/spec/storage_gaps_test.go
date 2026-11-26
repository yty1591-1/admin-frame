package spec_test

import (
	"testing"

	"github.com/admin-frame/admin-frame/internal/configcenter/spec"
)

func TestMissingStorageRefs(t *testing.T) {
	cases := []struct {
		name   string
		values map[string]string
		want   []string
	}{
		{
			name:   "s3 缺 bucket 与 region",
			values: map[string]string{"storage.driver": "s3"},
			want:   []string{"platform/storage.s3.bucket", "platform/storage.s3.region"},
		},
		{
			name: "minio 只缺 region",
			values: map[string]string{
				"storage.driver": "minio", "storage.s3.bucket": "b",
			},
			want: []string{"platform/storage.s3.region"},
		},
		{
			name: "s3 已填齐",
			values: map[string]string{
				"storage.driver": "s3", "storage.s3.bucket": "b", "storage.s3.region": "r",
			},
			want: nil,
		},
		{
			name:   "memory 不要求桶参数",
			values: map[string]string{"storage.driver": "memory"},
			want:   nil,
		},
		{
			name:   "nil values",
			values: nil,
			want:   nil,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := spec.MissingStorageRefs(tc.values)
			if len(got) != len(tc.want) {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
			for i, want := range tc.want {
				if got[i] != want {
					t.Fatalf("got %v, want %v", got, tc.want)
				}
			}
		})
	}
}
