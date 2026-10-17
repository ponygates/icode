//go:build windows

package sandbox

import (
	"testing"
	"unsafe"
)

func TestEnvBlockShape(t *testing.T) {
	b := envBlock([]string{"A=1", "B=2"})
	if b == nil {
		t.Fatal("nil block")
	}
	var got []uint16
	p := unsafe.Pointer(b)
	for i := 0; i < 16; i++ {
		got = append(got, *(*uint16)(unsafe.Pointer(uintptr(p) + uintptr(i)*2)))
	}
	t.Logf("first 16 units: %v", got)
	if got[0] != 'A' || got[1] != '=' || got[2] != '1' || got[3] != 0 {
		t.Fatalf("entry 1 malformed: %v", got[:4])
	}
	if got[4] != 'B' || got[5] != '=' || got[6] != '2' || got[7] != 0 {
		t.Fatalf("entry 2 malformed: %v", got[4:8])
	}
	if got[8] != 0 {
		t.Fatalf("block not double-NUL terminated: got[8]=%d", got[8])
	}
}
