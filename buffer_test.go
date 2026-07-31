package schema

import "testing"

func TestRoundtrip(tb *testing.T) {
	for _, tc := range []struct {
		in, out string
	}{
		{in: `null`},
		{in: `true`},
		{in: `false`},
		{in: `123`},
		{in: `-1.5e3`},
		{in: `"hi"`},
		{in: `"a\"b\n"`},
		{in: `[]`},
		{in: `[1,2,3]`},
		{in: `{}`},
		{in: `{"a":1}`},
		{in: "  { \"a\" : 1 , \"b\" : [ true, null ] }  ", out: `{"a":1,"b":[true,null]}`},
		{in: `{"a":{"b":{"c":[1,"x"]}}}`},
	} {
		want := tc.out
		if want == "" {
			want = tc.in
		}

		var b Buffer

		root, err := b.decode([]byte(tc.in))
		if err != nil {
			tb.Errorf("decode %q: %v", tc.in, err)
			continue
		}

		got := string(b.Reader().AppendJSON(nil, root))
		if got != want {
			tb.Errorf("roundtrip %q: got %q, want %q", tc.in, got, want)
		}
	}
}

func TestDecodeError(tb *testing.T) {
	for _, in := range []string{
		``,
		`{`,
		`[1,2`,
		`{"a":}`,
		`tru`,
		`1 2`,
		`{"a":1} x`,
	} {
		var b Buffer

		_, err := b.decode([]byte(in))
		if err == nil {
			tb.Errorf("decode %q: want error", in)
		}
	}
}

func TestFromJSON(tb *testing.T) {
	for _, in := range []string{
		`5`,
		`"x"`,
		`true`,
		`null`,
		`[1,2,3]`,
		`{"a":1,"b":["x",2]}`,
	} {
		var b Buffer

		root, err := b.Writer().FromJSON([]byte(in))
		if err != nil {
			tb.Errorf("fromjson %q: %v", in, err)
			continue
		}

		if got := string(b.Reader().AppendJSON(nil, root)); got != in {
			tb.Errorf("roundtrip %q: got %q", in, got)
		}
	}
}

func TestReuse(tb *testing.T) {
	var b Buffer

	root, err := b.decode([]byte(`[1,2,3]`))
	if err != nil {
		tb.Fatal(err)
	}

	if got := string(b.Reader().AppendJSON(nil, root)); got != `[1,2,3]` {
		tb.Fatalf("first: %q", got)
	}

	root, err = b.decode([]byte(`{"x":true}`))
	if err != nil {
		tb.Fatal(err)
	}

	if got := string(b.Reader().AppendJSON(nil, root)); got != `{"x":true}` {
		tb.Fatalf("reuse: %q", got)
	}
}

// TestSource covers the split between a node's bytes and its origin: decoded
// nodes report their span in src, synthesized ones report no source at all.
func TestSource(tb *testing.T) {
	var b Buffer

	b.Reset()

	src := []byte(`{"a":[1,"x",null]}`)

	root, err := b.decode(src)
	if err != nil {
		tb.Fatalf("decode: %v", err)
	}

	r, w := b.Reader(), b.Writer()

	for _, tc := range []struct {
		op   Opcode
		span string
	}{
		{root, `{"a":[1,"x",null]}`},
		{r.Nodes(root)[0], `"a"`},
		{r.Nodes(root)[1], `[1,"x",null]`},
		{r.Nodes(r.Nodes(root)[1])[0], `1`},
		{r.Nodes(r.Nodes(root)[1])[2], `null`},
	} {
		off, end, ok := r.Source(tc.op)
		if !ok || string(src[off:end]) != tc.span {
			tb.Errorf("source of %v: %d:%d ok=%v, want %q", tc.op.Op(), off, end, ok, tc.span)
		}
	}

	// Bare Bool/Null words are absent here on purpose: they carry no span, so they
	// read as position 0 and are indistinguishable from decoded ones.
	for _, op := range []Opcode{
		w.Int(5), w.Float(1.5), w.String("x"),
		w.Array(w.Int(1)), w.Object(w.String("k"), w.Int(1)), None,
	} {
		if off, end, ok := r.Source(op); ok {
			tb.Errorf("source of synthesized %v: %d:%d ok=true, want no source", op.Op(), off, end)
		}
	}

	// Span still panics on a word that never carried bytes.
	func() {
		defer func() {
			if recover() == nil {
				tb.Errorf("Span(IntLit): no panic")
			}
		}()

		r.Span(w.Int(5))
	}()
}

// TestCopyFrom copies a value across arenas, including synthesized words that
// carry their value in the opcode and so have no bytes to copy.
func TestCopyFrom(tb *testing.T) {
	var src, dst Buffer

	src.Reset()
	dst.Reset()

	if _, err := src.decode([]byte(`{"a":1}`)); err != nil {
		tb.Fatalf("decode: %v", err)
	}

	sr, sw, dw := src.Reader(), src.Writer(), dst.Writer()

	val := sw.Object(
		sw.String("i"), sw.Int(5),
		sw.String("f"), sw.Float(1.5),
		sw.String("s"), sw.String("x"),
		sw.String("a"), sw.Array(sw.Int(1), sw.Null(), sw.Bool(true)),
	)

	cp := dw.CopyFrom(sr, val)

	want := `{"i":5,"f":1.5,"s":"x","a":[1,null,true]}`
	if got := string(dst.Reader().AppendJSON(nil, cp)); got != want {
		tb.Errorf("copy: got %s, want %s", got, want)
	}
}
