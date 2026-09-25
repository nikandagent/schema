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
// Whatever the kind, src[off:end] is the token as written — quotes included.
func TestSource(tb *testing.T) {
	var b Buffer

	b.Reset()

	src := []byte(`["",{"a":[1,"x",null,true,false,-1.5e3,{"k":1},["z"]]},"a\"b"]`)

	root, err := b.decode(src)
	if err != nil {
		tb.Fatalf("decode: %v", err)
	}

	r, w := b.Reader(), b.Writer()

	top := r.Nodes(root)
	obj := r.Nodes(top[1])
	arr := r.Nodes(obj[1])

	for _, tc := range []struct {
		op    Node
		token string
	}{
		{root, string(src)},
		{top[0], `""`},
		{obj[0], `"a"`},
		{obj[1], `[1,"x",null,true,false,-1.5e3,{"k":1},["z"]]`},
		{arr[0], `1`},
		{arr[1], `"x"`},
		{arr[2], `null`},
		{arr[3], `true`},
		{arr[4], `false`},
		{arr[5], `-1.5e3`},
		{arr[6], `{"k":1}`},
		{arr[7], `["z"]`},
	} {
		off, end, ok := tc.op.Src()
		if !ok || string(src[off:end]) != tc.token {
			tb.Errorf("source of %v: %d:%d ok=%v %q, want %q", tc.op.Op(), off, end, ok, src[off:end], tc.token)
		}
	}

	// an empty string at the very start of a document spans its two quotes, which
	// is not the (0,0) that means "no source position"
	{
		var q Buffer

		q.Reset()

		root, err := q.decode([]byte(`""`))
		if err != nil {
			tb.Fatalf("decode: %v", err)
		}

		if off, end, ok := root.Src(); !ok || off != 0 || end != 2 {
			tb.Errorf("source of a lone empty string: %d:%d ok=%v, want 0:2 true", off, end, ok)
		}
	}

	// a string that had escapes was decoded into the text tail and still knows
	// which token it came from
	if off, end, ok := top[2].Src(); !ok || string(src[off:end]) != `"a\"b"` {
		tb.Errorf("source of an escaped string: %d:%d ok=%v %q, want the token", off, end, ok, src[off:end])
	}

	// Bare Bool/Null words are absent here on purpose: they carry no span, so they
	// read as position 0 and are indistinguishable from decoded ones.
	for _, op := range []Node{
		w.Int(5), w.Float(1.5), w.String("x"),
		w.Array(w.Int(1)), w.Object(w.String("k"), w.Int(1)), Node{},
	} {
		if off, end, ok := op.Src(); ok {
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

func TestSourceEscaped(tb *testing.T) {
	var b Buffer

	b.Reset()

	src := []byte(`["ab","a\u0062"]`)

	root, err := b.decode(src)
	if err != nil {
		tb.Fatalf("decode: %v", err)
	}

	r := b.Reader()
	plain, esc := r.Nodes(root)[0], r.Nodes(root)[1]

	off, end, ok := plain.Src()
	if !ok || string(src[off:end]) != `"ab"` {
		tb.Errorf("source of plain string: %d:%d ok=%v, want the token", off, end, ok)
	}

	// decoding moved it to the text tail, and it still points at its token
	if off, end, ok := esc.Src(); !ok || string(src[off:end]) != `"a\u0062"` {
		tb.Errorf("source of escaped string: %d:%d ok=%v %q, want the token", off, end, ok, src[off:end])
	}

	for _, op := range []Node{plain, esc} {
		if got := string(r.String(op)); got != "ab" {
			tb.Errorf("string: got %q, want %q", got, "ab")
		}
	}
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
