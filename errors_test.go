package schema

import (
	"errors"
	"fmt"
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
		if !(e.Off > 0 && e.End-e.Off == len("#/$defs/missing")) {
			tb.Errorf(`$ref missing: Off=%d End=%d, want Off>0 len=%d`, e.Off, e.End, len("#/$defs/missing"))
		}
	}
	{
		var s Schema
		err := s.Compile([]byte(`{"minLength":"x"}`))

		var e *Error
		if !errors.As(err, &e) {
			tb.Fatalf(`minLength: err %v is not *Error`, err)
		}
		if !(e.Off > 0 && e.End > e.Off) {
			tb.Errorf(`minLength: Off=%d End=%d, want Off>0 End>Off`, e.Off, e.End)
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

func TestInvalid(tb *testing.T) {
	if err := AsError(nil); err != nil {
		tb.Errorf("AsError(nil) = %v, want nil", err)
	}
	if err := AsError([]Diag{}); err != nil {
		tb.Errorf("AsError([]Diag{}) = %v, want nil", err)
	}

	var s Schema
	if err := s.Compile([]byte(`{"required":["a","b"]}`)); err != nil {
		tb.Fatalf("compile: %v", err)
	}

	diags, err := s.Validate([]byte(`{}`))
	if err != nil {
		tb.Fatalf("validate: %v", err)
	}
	if len(diags) < 2 {
		tb.Fatalf("diag count %d, want >=2", len(diags))
	}

	err = AsError(diags)
	if err == nil {
		tb.Fatalf("AsError(%d diags) = nil, want error", len(diags))
	}

	var inv Invalid
	if !errors.As(err, &inv) {
		tb.Errorf("errors.As(err, &inv) = false")
	}
	if len(inv) != len(diags) {
		tb.Errorf("len(inv)=%d, want %d", len(inv), len(diags))
	}

	if got := AsDiag(err); len(got) != len(diags) {
		tb.Errorf("AsDiag len=%d, want %d", len(got), len(diags))
	}

	if got := AsDiag(errors.New("x")); got != nil {
		tb.Errorf("AsDiag(other error) = %v, want nil", got)
	}

	if msg := err.Error(); msg == "" || !strings.Contains(msg, "invalid document") {
		tb.Errorf("Error() = %q, want non-empty containing %q", msg, "invalid document")
	}

	// AsDiag sees through wrapping.
	wrapped := fmt.Errorf("ctx: %w", AsError(diags))
	if got := AsDiag(wrapped); len(got) != len(diags) {
		tb.Errorf("AsDiag(wrapped) len=%d, want %d", len(got), len(diags))
	}
}

func TestFormatNicely(tb *testing.T) {
	one := func(src, data string) Diag {
		var s Schema
		if err := s.Compile([]byte(src)); err != nil {
			tb.Fatalf("compile %q: %v", src, err)
		}

		diag, err := s.Validate([]byte(data))
		if err != nil {
			tb.Fatalf("validate %q: %v", data, err)
		}
		if len(diag) != 1 {
			tb.Fatalf("validate %q: diag count %d, want 1: %+v", data, len(diag), diag)
		}

		return diag[0]
	}

	// A. Both sides elided.
	{
		data := `{"a":1,"n":12345,"z":9}`
		d := one(`{"properties":{"n":{"type":"string"}}}`, data)

		got := string(d.FormatNicelyContext(nil, []byte(data), 3, 3))
		want := "...n\":12345,\"z...\n      ^ Wrong type\n"
		if got != want {
			tb.Errorf("A: got %q, want %q", got, want)
		}
	}

	// B. Wide context, nothing elided.
	{
		data := `{"tags":[1]}`
		d := one(`{"properties":{"tags":{"type":"array","minItems":2}}}`, data)

		got := string(d.FormatNicelyContext(nil, []byte(data), 50, 50))
		want := "{\"tags\":[1]}\n        ^ Too few items\n"
		if got != want {
			tb.Errorf("B: got %q, want %q", got, want)
		}
		if strings.Contains(got, "...") {
			tb.Errorf("B: unexpected elision: %q", got)
		}
		if got[0] != data[0] {
			tb.Errorf("B: line starts at %q, want %q", got[0], data[0])
		}
	}

	// C. Message capitalized, rest unchanged.
	{
		data := `{"tags":[1]}`
		d := one(`{"properties":{"tags":{"type":"array","minItems":2}}}`, data)
		if d.Msg != "too few items" {
			tb.Fatalf("C: base message %q, want %q", d.Msg, "too few items")
		}

		got := string(d.FormatNicelyContext(nil, []byte(data), 50, 50))
		if !strings.Contains(got, "^ Too few items\n") {
			tb.Errorf("C: capitalized message missing: %q", got)
		}
	}

	// D. Invalid separates snippets with a blank line.
	{
		var s Schema
		if err := s.Compile([]byte(`{"required":["a","b"]}`)); err != nil {
			tb.Fatalf("compile: %v", err)
		}

		data := `{}`
		diag, err := s.Validate([]byte(data))
		if err != nil {
			tb.Fatalf("validate: %v", err)
		}
		if len(diag) != 2 {
			tb.Fatalf("D: diag count %d, want 2", len(diag))
		}

		got := string(Invalid(diag).FormatNicelyContext(nil, []byte(data), 5, 5))
		if n := strings.Count(got, "\n\n"); n != 1 {
			tb.Errorf("D: %d blank-line separators, want 1: %q", n, got)
		}

		parts := strings.Split(got, "\n\n")
		if len(parts) != 2 {
			tb.Fatalf("D: split into %d parts, want 2: %q", len(parts), got)
		}
		for i, p := range parts {
			if !strings.Contains(p, "^ ") {
				tb.Errorf("D: part %d has no caret line: %q", i, p)
			}
		}
		if !strings.HasSuffix(got, "\n") {
			tb.Errorf("D: output does not end in newline: %q", got)
		}
	}

	// E. Clamping and oversized context must not panic.
	{
		got := string(Diag{Off: 0, End: 0, Msg: "no location"}.FormatNicelyContext(nil, nil, 5, 5))
		if !strings.Contains(got, "^ No location") {
			tb.Errorf("E empty: %q", got)
		}

		got = string(Diag{Off: 2, End: 100, Msg: "past end"}.FormatNicelyContext(nil, []byte(`{}`), 5, 5))
		if !strings.Contains(got, "^ Past end") {
			tb.Errorf("E overrun: %q", got)
		}

		// Large before/after on a short src: caret indent stays small because start
		// clamps to 0, so pad never approaches the 128-wide spaces constant.
		got = string(Diag{Off: 1, End: 2, Msg: "wide"}.FormatNicelyContext(nil, []byte(`{}`), 1000, 1000))
		lines := strings.SplitN(got, "\n", 2)
		if indent := strings.IndexByte(lines[1], '^'); indent != 1 {
			tb.Errorf("E wide: caret indent %d, want 1 (stayed within 128): %q", indent, got)
		}
	}
}
