package schema

import (
	"errors"
	"strings"
	"testing"
)

func TestError(tb *testing.T) {
	for _, tc := range []struct {
		in   string
		want error
		msg  string
		pref bool // msg is a prefix
	}{
		{`{"minLength":"x"}`, ErrKeyword, `"minLength" must be an integer`, false},
		{`{"type":123}`, ErrKeyword, `"type" must be a string or array of type names`, false},
		{`{"uniqueItems":1}`, ErrKeyword, "", false},
		{`{"pattern":"("}`, ErrPattern, `"(" is not a valid regular expression:`, true},
		{`{"$ref":123}`, ErrKeyword, "", false},
		{`{"$ref":""}`, ErrKeyword, "", false},
		{`{"$ref":"#/$defs/missing"}`, ErrRef, `"#/$defs/missing"`, false},
		{`123`, ErrKeyword, `a schema must be an object or a boolean`, false},
	} {
		var s Schema

		err := s.Compile([]byte(tc.in))
		if err == nil {
			tb.Errorf("compile %q: want error", tc.in)
			continue
		}

		var e *Error
		if !errors.As(err, &e) {
			tb.Errorf("compile %q: err %v (%T) is not *Error", tc.in, err, err)
			continue
		}

		if !errors.Is(err, tc.want) {
			tb.Errorf("compile %q: err %v, want Is(%v)", tc.in, err, tc.want)
		}
		if e.Message == "" {
			tb.Errorf("compile %q: empty Message", tc.in)
		}
		if e.Err == nil {
			tb.Errorf("compile %q: nil Err", tc.in)
		}

		if tc.msg != "" {
			if tc.pref {
				if !strings.HasPrefix(e.Message, tc.msg) {
					tb.Errorf("compile %q: Message %q, want prefix %q", tc.in, e.Message, tc.msg)
				}
			} else if e.Message != tc.msg {
				tb.Errorf("compile %q: Message %q, want %q", tc.in, e.Message, tc.msg)
			}
		}
	}

	// SchemaRejectUnknown surfaces an unknown-keyword *Error.
	{
		s := Schema{Flags: SchemaRejectUnknown}

		err := s.Compile([]byte(`{"nope":1}`))
		var e *Error
		if !errors.As(err, &e) || !errors.Is(err, ErrUnknownKeyword) {
			tb.Errorf(`compile {"nope":1} rejectUnknown: err %v, want ErrUnknownKeyword *Error`, err)
		}
	}

	// Position spans for a couple of cases.
	{
		var s Schema
		err := s.Compile([]byte(`{"$ref":"#/$defs/missing"}`))

		var e *Error
		if !errors.As(err, &e) {
			tb.Fatalf(`$ref missing: err %v is not *Error`, err)
		}
		if !(e.Off > 0 && e.Len == len("#/$defs/missing")) {
			tb.Errorf(`$ref missing: Off=%d Len=%d, want Off>0 Len=%d`, e.Off, e.Len, len("#/$defs/missing"))
		}
	}
	{
		var s Schema
		err := s.Compile([]byte(`{"minLength":"x"}`))

		var e *Error
		if !errors.As(err, &e) {
			tb.Fatalf(`minLength: err %v is not *Error`, err)
		}
		if !(e.Off > 0 && e.Len > 0) {
			tb.Errorf(`minLength: Off=%d Len=%d, want Off>0 Len>0`, e.Off, e.Len)
		}
	}

	// Scope boundary: pure JSON-shape failures are not wrapped into *Error.
	for _, tc := range []struct {
		in   string
		want error
	}{
		// `{` is a truncated prefix; Compile normalizes json2's short-buffer
		// signal to ErrSyntax (malformed input in a complete document). The
		// load-bearing check is that these are NOT wrapped into *Error.
		{`{`, ErrSyntax},
		{`{} junk`, ErrTrailingData},
	} {
		var s Schema
		err := s.Compile([]byte(tc.in))

		var e *Error
		if errors.As(err, &e) {
			tb.Errorf("compile %q: unexpectedly wrapped into *Error: %v", tc.in, err)
		}
		if !errors.Is(err, tc.want) {
			tb.Errorf("compile %q: err %v, want Is(%v)", tc.in, err, tc.want)
		}
	}
}

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
