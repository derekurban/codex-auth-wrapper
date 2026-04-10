//go:build windows

package conpty

import "testing"

func TestStripControlCConsumesInterruptAndPreservesOtherInput(t *testing.T) {
	calls := 0
	input := []byte{'a', 0x03, 'b', 0x03, 'c'}
	got := stripControlC(input, func() { calls++ })
	if string(got) != "abc" {
		t.Fatalf("expected filtered input %q, got %q", "abc", string(got))
	}
	if calls != 2 {
		t.Fatalf("expected 2 ctrl+c callbacks, got %d", calls)
	}
}
