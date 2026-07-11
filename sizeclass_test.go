package schema

import (
	"testing"
	"unsafe"
)

// Go runtime span size classes (runtime/sizeclasses.go). A small-object
// allocation rounds up to one of these, so a struct a few bytes over a boundary
// wastes the whole next step — e.g. 928 lands in 1024, wasting 96.
var spanClasses = []uintptr{
	8, 16, 24, 32, 48, 64, 80, 96, 112, 128, 144, 160, 176, 192, 208, 224, 240,
	256, 288, 320, 352, 384, 416, 448, 480, 512, 576, 640, 704, 768, 896, 1024,
	1152, 1280, 1408, 1536, 1792, 2048, 2304, 2688, 3072, 3200,
}

func spanClass(n uintptr) (cls, waste uintptr) {
	for _, c := range spanClasses {
		if n <= c {
			return c, c - n
		}
	}

	return n, 0 // large objects are page-rounded; out of scope here
}

// TestSizeClasses guards the hot heap-allocated structs against silently
// overshooting a span bucket, since a few bytes over a boundary throw away the
// whole next step. It warns on any slack and fails on a big chunk, so a future
// field addition that tips Applier from 896 into 1024 shows up as a red test
// rather than a quiet 96-byte-per-Applier regression.
func TestSizeClasses(tb *testing.T) {
	const (
		warn = 16 // a couple of Opcodes; nudge an inline buffer to fill it
		fail = 64 // a big chunk — resize a buffer to fill the bucket or drop under it
	)

	for _, s := range []struct {
		name string
		size uintptr
	}{
		{"Buffer", unsafe.Sizeof(Buffer{})},
		{"Applier", unsafe.Sizeof(Applier{})},
		{"Schema", unsafe.Sizeof(Schema{})},
	} {
		cls, waste := spanClass(s.size)

		switch {
		case waste > fail:
			tb.Errorf("%s = %d B wastes %d B in the %d bucket (>%d): grow an inline buffer to fill it, or trim under the boundary",
				s.name, s.size, waste, cls, fail)
		case waste > warn:
			tb.Logf("warning: %s = %d B wastes %d B in the %d bucket", s.name, s.size, waste, cls)
		default:
			tb.Logf("%s = %d B -> %d bucket (waste %d)", s.name, s.size, cls, waste)
		}
	}
}
