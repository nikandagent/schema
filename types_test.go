package schema

import "testing"

func TestTypesOfKeyword(tb *testing.T) {
	all := TypeNull | TypeBoolean | TypeInteger | TypeNumber | TypeString | TypeArray | TypeObject

	for _, tc := range []struct {
		in   string
		want Types
	}{
		{`{}`, 0}, // absent keyword: Keyword gives None, TypesOf gives the empty set
		{`{"type":"null"}`, TypeNull},
		{`{"type":"boolean"}`, TypeBoolean},
		{`{"type":"integer"}`, TypeInteger},
		{`{"type":"number"}`, TypeNumber},
		{`{"type":"string"}`, TypeString},
		{`{"type":"array"}`, TypeArray},
		{`{"type":"object"}`, TypeObject},
		{`{"type":["string","integer"]}`, TypeInteger | TypeString},
		{`{"type":["null","boolean","integer","number","string","array","object"]}`, all},
	} {
		s, err := Compile([]byte(tc.in))
		if err != nil {
			tb.Errorf("compile %q: %v", tc.in, err)
			continue
		}

		b := s.Reader()

		got := TypesOf(b.Keyword(s.Root(), Type))
		if got != tc.want {
			tb.Errorf("typesof %q: got %v (%b), want %v (%b)", tc.in, got, got, tc.want, tc.want)
		}
	}
}

func TestTypesOfPanic(tb *testing.T) {
	s, err := Compile([]byte(`{"minLength":2,"properties":{"a":{}}}`))
	if err != nil {
		tb.Fatal(err)
	}

	b := s.Reader()

	mustPanic(tb, "TypesOf(MinLen)", func() { TypesOf(b.Keyword(s.Root(), MinLen)) })
	mustPanic(tb, "TypesOf(Properties)", func() { TypesOf(b.Keyword(s.Root(), Properties)) })
	mustPanic(tb, "TypesOf(All)", func() { TypesOf(s.Root()) })

	var buf Buffer

	root, err := buf.decode([]byte(`{"a":"x","b":1}`))
	if err != nil {
		tb.Fatalf("decode: %v", err)
	}

	r, w := buf.Reader(), buf.Writer()

	for _, tc := range []struct {
		name string
		op   Opcode
	}{
		{"decoded Object", root},
		{"decoded String", r.Nodes(root)[0]},
		{"decoded Number", r.Nodes(root)[3]},
		{"IntLit", w.Int(5)},
		{"FltLit", w.Float(1.5)},
		{"String", w.String("x")},
		{"Null", w.Null()},
		{"True", w.Bool(true)},
		{"Array", w.Array()},
	} {
		mustPanic(tb, "TypesOf("+tc.name+")", func() { TypesOf(tc.op) })
	}
}

func TestTypesString(tb *testing.T) {
	all := TypeNull | TypeBoolean | TypeInteger | TypeNumber | TypeString | TypeArray | TypeObject

	for _, tc := range []struct {
		t    Types
		want string
	}{
		{0, "empty"},
		{TypeNull, "null"},
		{TypeBoolean, "boolean"},
		{TypeInteger, "integer"},
		{TypeNumber, "number"},
		{TypeString, "string"},
		{TypeArray, "array"},
		{TypeObject, "object"},
		{TypeString | TypeInteger, "integer|string"}, // declared bit order, not argument order
		{all, "null|boolean|integer|number|string|array|object"},
	} {
		if got := tc.t.String(); got != tc.want {
			tb.Errorf("string %b: got %q, want %q", tc.t, got, tc.want)
		}
	}
}

func TestTypesIsAny(tb *testing.T) {
	t := TypeInteger | TypeString

	for _, tc := range []struct {
		g        Types
		is, some bool
	}{
		{0, true, false},
		{TypeInteger, true, true},
		{TypeString, true, true},
		{TypeInteger | TypeString, true, true},
		{TypeInteger | TypeNumber, false, true},
		{TypeNumber, false, false},
		{TypeNumber | TypeObject, false, false},
	} {
		if got := t.Is(tc.g); got != tc.is {
			tb.Errorf("%v.Is(%v): got %v, want %v", t, tc.g, got, tc.is)
		}
		if got := t.Any(tc.g); got != tc.some {
			tb.Errorf("%v.Any(%v): got %v, want %v", t, tc.g, got, tc.some)
		}
	}
}
