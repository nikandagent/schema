package schema

import "testing"

func TestCompileError(tb *testing.T) {
	for _, in := range []string{
		`123`,
		`"x"`,
		`{`,
		`{"type":}`,
		`{} junk`,
		`{"$ref":"#/$defs/missing"}`,
		`{"$ref":"http://example.com/x"}`,
		`{"pattern":"("}`,                // invalid regex
		`{"patternProperties":{"(":{}}}`, // invalid regex key
		`{"$defs":{"a/b":{"type":"integer"}},"$ref":"#/$defs/a/b"}`, // literal slash is navigation, not the escaped name
	} {
		var s Schema

		if err := s.Compile([]byte(in)); err == nil {
			tb.Errorf("compile %q: want error", in)
		}
	}
}

func TestFlags(tb *testing.T) {
	var f Flags

	if f.Is(KeepMissing) {
		tb.Errorf("zero Is(KeepMissing) = true")
	}

	f.Set(DataPreserve)
	if !f.Is(KeepKeyOrder) || !f.Is(KeepMissing) || !f.Is(DataPreserve) {
		tb.Errorf("after Set(DataPreserve): %b", f)
	}

	f.Unset(KeepKeyOrder)
	if f.Is(KeepKeyOrder) || !f.Is(KeepMissing) {
		tb.Errorf("after Unset(KeepKeyOrder): %b", f)
	}
}

func TestRejectUnknownKeywords(tb *testing.T) {
	// spec default keeps unknowns; the flag rejects typos but not known keywords.
	for _, tc := range []struct {
		in    string
		flags Flags
		ok    bool
	}{
		{`{"nope":1}`, 0, true},
		{`{"nope":1}`, SchemaRejectUnknown, false},
		{`{"if":{"type":"string"}}`, SchemaRejectUnknown, true}, // known-but-unsupported, kept
	} {
		s := Schema{Flags: tc.flags}

		err := s.Compile([]byte(tc.in))
		if (err == nil) != tc.ok {
			tb.Errorf("compile %q flags=%b: ok=%v, err=%v", tc.in, tc.flags, tc.ok, err)
		}
	}
}
