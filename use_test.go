package schema

import (
	"sync"
	"testing"
)

// TestUse covers the Use option: a caller-supplied Applier used in place of the
// shared s.c, so the same *Schema can be walked concurrently. It also guards the
// reset: a reused Applier must start clean each call (walk resets whichever
// Applier it walks, not only the default one).
func TestUse(tb *testing.T) {
	s, err := Compile([]byte(`{"properties":{"a":{"type":"string"}}}`))
	if err != nil {
		tb.Fatal(err)
	}

	var a Applier // one Applier, reused across calls below

	// invalid then valid through the same Applier: the second call must not
	// carry the first call's diagnostic.
	if d, err := s.Validate([]byte(`{"a":1}`), Use(&a)); err != nil || len(d) != 1 {
		tb.Fatalf("invalid: diag=%d err=%v, want 1/nil", len(d), err)
	}
	if d, err := s.Validate([]byte(`{"a":"ok"}`), Use(&a)); err != nil || len(d) != 0 {
		tb.Fatalf("valid after reuse: diag=%d err=%v, want 0/nil (reset leaked)", len(d), err)
	}

	// rewrite through Use must actually rewrite (proves the rewrite flag is set
	// on the provided Applier, not just the default).
	out, _, err := s.WalkRewrite(nil, []byte(`{ "a" : "x" }`), nil, Use(&a))
	if err != nil || string(out) != `{"a":"x"}` {
		tb.Fatalf("rewrite through Use: out=%q err=%v", out, err)
	}
}

// TestUseParallel validates the same compiled *Schema from many goroutines, each
// with its own Applier, under -race.
func TestUseParallel(tb *testing.T) {
	s, err := Compile([]byte(`{"properties":{"a":{"type":"string"}}}`))
	if err != nil {
		tb.Fatal(err)
	}

	docs := []string{`{"a":"ok"}`, `{"a":1}`}
	wants := []int{0, 1}

	var wg sync.WaitGroup

	for g := range 8 {
		wg.Add(1)

		go func(g int) {
			defer wg.Done()

			var a Applier // per-goroutine, never shared

			for i := range 300 {
				k := i % 2

				d, err := s.Validate([]byte(docs[k]), Use(&a))
				if err != nil {
					tb.Errorf("g%d: %v", g, err)
					return
				}

				if len(d) != wants[k] {
					tb.Errorf("g%d i%d: diag=%d want %d", g, i, len(d), wants[k])
					return
				}
			}
		}(g)
	}

	wg.Wait()
}
