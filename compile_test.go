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
	} {
		var s Schema

		if err := s.Compile([]byte(in)); err == nil {
			tb.Errorf("compile %q: want error", in)
		}
	}
}

func TestRejectUnknownKeywords(tb *testing.T) {
	// spec default keeps unknowns; the flag rejects typos but not known keywords.
	for _, tc := range []struct {
		in     string
		reject bool
		ok     bool
	}{
		{`{"nope":1}`, false, true},
		{`{"nope":1}`, true, false},
		{`{"if":{"type":"string"}}`, true, true}, // known-but-unsupported, kept
	} {
		s := Schema{RejectUnknownKeywords: tc.reject}

		err := s.Compile([]byte(tc.in))
		if (err == nil) != tc.ok {
			tb.Errorf("compile %q reject=%v: ok=%v, err=%v", tc.in, tc.reject, tc.ok, err)
		}
	}
}
