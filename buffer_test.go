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

		got := string(b.AppendJSON(nil, root))
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

		root, err := b.FromJSON([]byte(in))
		if err != nil {
			tb.Errorf("fromjson %q: %v", in, err)
			continue
		}

		if got := string(b.AppendJSON(nil, root)); got != in {
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

	if got := string(b.AppendJSON(nil, root)); got != `[1,2,3]` {
		tb.Fatalf("first: %q", got)
	}

	root, err = b.decode([]byte(`{"x":true}`))
	if err != nil {
		tb.Fatal(err)
	}

	if got := string(b.AppendJSON(nil, root)); got != `{"x":true}` {
		tb.Fatalf("reuse: %q", got)
	}
}
