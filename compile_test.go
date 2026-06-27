package schema

import "testing"

func TestCompileError(tb *testing.T) {
	for _, in := range []string{
		`123`,
		`"x"`,
		`{`,
		`{"type":}`,
		`{"nope":1}`,
		`{} junk`,
	} {
		var s Schema

		if err := s.Compile([]byte(in)); err == nil {
			tb.Errorf("compile %q: want error", in)
		}
	}
}
