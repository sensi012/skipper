package local

import "testing"

func TestKindManagerName(t *testing.T) {
	km := NewKindManager()
	if km.Name() != "kind" {
		t.Fatalf("expected kind, got %s", km.Name())
	}
}
