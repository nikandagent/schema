package schema

import "testing"

func mustPanic(tb *testing.T, name string, f func()) {
	tb.Helper()

	defer func() {
		if recover() == nil {
			tb.Errorf("%s: expected panic", name)
		}
	}()

	f()
}

func TestBufferNodesLen(tb *testing.T) {
	s, err := Compile([]byte(`{"type":"string","x-type":"custom","properties":{"a":{},"b":{},"c":{}}}`))
	if err != nil {
		tb.Fatal(err)
	}

	b := s.SchemaBuf()

	ext := b.Keyword(s.Root(), Ext)
	if n := b.NodesLen(ext); n != 1 {
		tb.Errorf("ext len: got %d, want 1", n)
	}

	props := b.Keyword(s.Root(), Properties)
	if n := b.NodesLen(props); n != 3 {
		tb.Errorf("properties len: got %d, want 3", n)
	}
}

func TestBufferNodesAt(tb *testing.T) {
	s, err := Compile([]byte(`{"x-type":"custom","properties":{"a":{},"b":{}}}`))
	if err != nil {
		tb.Fatal(err)
	}

	b := s.SchemaBuf()

	ext := b.Keyword(s.Root(), Ext)

	k, v := b.NodesAt(ext, 0)
	if got := string(b.String(k)); got != "x-type" {
		tb.Errorf("ext key: got %q, want %q", got, "x-type")
	}
	if got := string(b.String(v)); got != "custom" {
		tb.Errorf("ext value: got %q, want %q", got, "custom")
	}

	if k, v := b.NodesAt(ext, 1); k != None || v != None {
		tb.Errorf("ext at 1: got %v/%v, want None/None", k, v)
	}
	if k, v := b.NodesAt(ext, -2); k != None || v != None {
		tb.Errorf("ext at -2: got %v/%v, want None/None", k, v)
	}

	props := b.Keyword(s.Root(), Properties)

	k, _ = b.NodesAt(props, -1)
	if got := string(b.Span(k)); got != `"b"` {
		tb.Errorf("properties at -1 key: got %q, want %q", got, `"b"`)
	}
}

func TestBufferNodesPanic(tb *testing.T) {
	s, err := Compile([]byte(`{"type":"string","minimum":3}`))
	if err != nil {
		tb.Fatal(err)
	}

	b := s.SchemaBuf()

	typ := b.Keyword(s.Root(), Type)
	mustPanic(tb, "Nodes(Type)", func() { b.Nodes(typ) })
	mustPanic(tb, "NodesAt(Type)", func() { b.NodesAt(typ, 0) })

	num := b.Deref(b.Keyword(s.Root(), Minimum))
	mustPanic(tb, "Nodes(Number)", func() { b.Nodes(num) })
	mustPanic(tb, "NodesAt(Number)", func() { b.NodesAt(num, 0) })
}

func TestBufferExt(tb *testing.T) {
	s, err := Compile([]byte(`{"type":"string","x-type":"custom"}`))
	if err != nil {
		tb.Fatal(err)
	}

	b := s.SchemaBuf()

	if v := b.Ext(s.Root(), "x-type"); string(b.String(v)) != "custom" {
		tb.Errorf("ext x-type: got %q, want %q", b.String(v), "custom")
	}

	if v := b.Ext(s.Root(), "x-missing"); v != None {
		tb.Errorf("ext x-missing: got %v, want None", v)
	}

	typ := b.Keyword(s.Root(), Type)
	mustPanic(tb, "Ext(Type)", func() { b.Ext(typ, "x-type") })
}

func TestBufferKeyword(tb *testing.T) {
	s, err := Compile([]byte(`{"type":"string"}`))
	if err != nil {
		tb.Fatal(err)
	}

	b := s.SchemaBuf()

	if op := b.Keyword(s.Root(), Type); op.Op() != Type {
		tb.Errorf("keyword Type: got %v", op.Op())
	}

	if op := b.Keyword(s.Root(), Minimum); op != None {
		tb.Errorf("keyword Minimum: got %v, want None", op)
	}

	typ := b.Keyword(s.Root(), Type)
	mustPanic(tb, "Keyword(Type)", func() { b.Keyword(typ, Type) })
}

func TestBufferDeref(tb *testing.T) {
	for _, tc := range []struct {
		schema string
		want   Opcode
		kind   Opcode
		span   string
	}{
		{`{"not":{"type":"string"}}`, Not, All, ""},
		{`{"items":{"type":"number"}}`, Items, All, ""},
		{`{"const":5}`, Const, Number, "5"},
		{`{"minimum":3}`, Minimum, Number, "3"},
	} {
		s, err := Compile([]byte(tc.schema))
		if err != nil {
			tb.Errorf("compile %q: %v", tc.schema, err)
			continue
		}

		b := s.SchemaBuf()

		ch := b.Deref(b.Keyword(s.Root(), tc.want))
		if ch.Op() != tc.kind {
			tb.Errorf("deref %q: got kind %v, want %v", tc.schema, ch.Op(), tc.kind)
		}
		if tc.span != "" && string(b.Span(ch)) != tc.span {
			tb.Errorf("deref %q: got span %q, want %q", tc.schema, b.Span(ch), tc.span)
		}
	}

	s, err := Compile([]byte(`{"properties":{"a":{}},"type":"string"}`))
	if err != nil {
		tb.Fatal(err)
	}

	b := s.SchemaBuf()

	mustPanic(tb, "Deref(Properties)", func() { b.Deref(b.Keyword(s.Root(), Properties)) })
	mustPanic(tb, "Deref(Type)", func() { b.Deref(b.Keyword(s.Root(), Type)) })
}
