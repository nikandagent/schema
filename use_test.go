package schema

import (
	"sync"
	"testing"
)

// TestUse covers reusing one Applier across calls: it must start clean each
// time, and carry the rewrite flag only for the call that asked for it.
func TestUse(tb *testing.T) {
	s, err := Compile([]byte(`{"properties":{"a":{"type":"string"}}}`))
	if err != nil {
		tb.Fatal(err)
	}

	var a Applier // one Applier, reused across calls below

	// invalid then valid through the same Applier: the second call must not
	// carry the first call's diagnostic.
	if d, err := a.Validate(s, []byte(`{"a":1}`)); err != nil || len(d) != 1 {
		tb.Fatalf("invalid: diag=%d err=%v, want 1/nil", len(d), err)
	}
	if d, err := a.Validate(s, []byte(`{"a":"ok"}`)); err != nil || len(d) != 0 {
		tb.Fatalf("valid after reuse: diag=%d err=%v, want 0/nil (reset leaked)", len(d), err)
	}

	out, _, err := a.Rewrite(s, Node{}, []byte(`{ "a" : "x" }`), nil, nil)
	if err != nil || string(out) != `{"a":"x"}` {
		tb.Fatalf("rewrite: out=%q err=%v", out, err)
	}

	if !a.Rewriting() {
		tb.Errorf("Rewriting() after a rewrite: false")
	}

	if d, err := a.Validate(s, []byte(`{"a":"ok"}`)); err != nil || len(d) != 0 || a.Rewriting() {
		tb.Errorf("validate after a rewrite: diag=%d err=%v rewriting=%v", len(d), err, a.Rewriting())
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

				d, err := a.Validate(s, []byte(docs[k]))
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
