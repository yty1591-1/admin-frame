package s3

import "testing"

func TestEndpointNeedsPathStyle(t *testing.T) {
	cases := []struct {
		endpoint string
		want     bool
	}{
		{"", false},
		{"https://s3.amazonaws.com", false},
		{"http://127.0.0.1:19000", true},
		{"http://localhost:9000", true},
		{"http://[::1]:9000", true},
		{"https://minio.example.com", false},
	}
	for _, tc := range cases {
		if got := endpointNeedsPathStyle(tc.endpoint); got != tc.want {
			t.Fatalf("endpointNeedsPathStyle(%q)=%v, want %v", tc.endpoint, got, tc.want)
		}
	}
}
