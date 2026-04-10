package crypto

import "testing"

func TestParseTLSVersion(t *testing.T) {
	tests := []struct {
		in      string
		wantErr bool
	}{
		{"1.2", false},
		{"1.3", false},
		{"tls1.2", false},
		{"tls1.3", false},
		{"", false},
		{"1.1", true},
	}
	for _, tc := range tests {
		_, err := parseTLSVersion(tc.in)
		if tc.wantErr && err == nil {
			t.Fatalf("expected error for %q", tc.in)
		}
		if !tc.wantErr && err != nil {
			t.Fatalf("unexpected error for %q: %v", tc.in, err)
		}
	}
}
