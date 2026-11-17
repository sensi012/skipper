package tunnel

import (
	"testing"
)

func TestParsePortString(t *testing.T) {
	tests := []struct {
		input     string
		expectedL int
		expectedR int
		hasErr    bool
	}{
		{"8080", 8080, 8080, false},
		{"3000:80", 3000, 80, false},
		{"invalid", 0, 0, true},
		{"abc:80", 0, 0, true},
	}

	for _, tc := range tests {
		l, r, err := ParsePortString(tc.input)
		if tc.hasErr && err == nil {
			t.Fatalf("expected error for %s", tc.input)
		}
		if !tc.hasErr && err != nil {
			t.Fatalf("unexpected error for %s: %v", tc.input, err)
		}
		if !tc.hasErr && (l != tc.expectedL || r != tc.expectedR) {
			t.Fatalf("for %s expected %d:%d, got %d:%d", tc.input, tc.expectedL, tc.expectedR, l, r)
		}
	}
}
