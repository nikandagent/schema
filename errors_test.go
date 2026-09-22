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
		code DiagCode
	}{
		{`{"minLength":"x"}`, ErrKeyword, MustBeInteger},
		{`{"type":123}`, ErrKeyword, InvalidTypeShape},
		{`{"uniqueItems":1}`, ErrKeyword, MustBeBool},
		{`{"pattern":"("}`, ErrPattern, BadPattern},
		{`{"$ref":123}`, ErrKeyword, MustBeString},
		{`{"$ref":""}`, ErrKeyword, EmptyRef},
		{`{"$ref":"#/$defs/missing"}`, ErrRef, UnresolvedRef},
		{`123`, ErrKeyword, SchemaMustBeObject},
	} {
		var s Schema

		err := s.Compile([]byte(tc.in))
		if err == nil {
			tb.Errorf("compile %q: want error", tc.in)
			continue
		}

		d := AsDiag(err)
		if len(d) != 1 {
			tb.Errorf("compile %q: err %v (%T) carries %d diags, want 1", tc.in, err, err, len(d))
			continue
		}

		if !errors.Is(err, tc.want) {
			tb.Errorf("compile %q: err %v, want Is(%v)", tc.in, err, tc.want)
		}
		if d[0].Code != tc.code {
			tb.Errorf("compile %q: Code %v, want %v", tc.in, d[0].Code, tc.code)
		}

		want := tc.want.Error() + ": " + tc.code.String()
		if err.Error() != want {
			tb.Errorf("compile %q: err %q, want %q", tc.in, err.Error(), want)
		}
	}

	// SchemaRejectUnknown surfaces an unknown-keyword Diagnostics.
	{
		s := Schema{Flags: SchemaRejectUnknown}

		err := s.Compile([]byte(`{"nope":1}`))
		if !errors.As(err, &Diagnostics{}) || !errors.Is(err, ErrUnknownKeyword) {
			tb.Errorf(`compile {"nope":1} rejectUnknown: err %v, want ErrUnknownKeyword Diagnostics`, err)
		}
	}

	// Position spans for a couple of cases.
	{
		var s Schema
		err := s.Compile([]byte(`{"$ref":"#/$defs/missing"}`))

		d := AsDiag(err)
		if len(d) != 1 {
			tb.Fatalf(`$ref missing: err %v is not Diagnostics`, err)
		}
		if !(d[0].Off > 0 && d[0].End-d[0].Off == len("#/$defs/missing")) {
			tb.Errorf(`$ref missing: Off=%d End=%d, want Off>0 len=%d`, d[0].Off, d[0].End, len("#/$defs/missing"))
		}
	}
	{
		var s Schema
		err := s.Compile([]byte(`{"minLength":"x"}`))

		d := AsDiag(err)
		if len(d) != 1 {
			tb.Fatalf(`minLength: err %v is not Diagnostics`, err)
		}
		if !(d[0].Off > 0 && d[0].End > d[0].Off) {
			tb.Errorf(`minLength: Off=%d End=%d, want Off>0 End>Off`, d[0].Off, d[0].End)
		}
		if d[0].Op.Op() != MinLen {
			tb.Errorf(`minLength: Op=%v, want MinLen`, d[0].Op.Op())
		}
	}

	// Scope boundary: pure JSON-shape failures are not wrapped into Diagnostics.
	for _, tc := range []struct {
		in   string
		want error
	}{
		// `{` is a truncated prefix; Compile normalizes json2's short-buffer
		// signal to ErrSyntax (malformed input in a complete document). The
		// load-bearing check is that these are NOT wrapped into Diagnostics.
		{`{`, ErrSyntax},
		{`{} junk`, ErrTrailingData},
	} {
		var s Schema
		err := s.Compile([]byte(tc.in))

		if d := AsDiag(err); d != nil {
			tb.Errorf("compile %q: unexpectedly wrapped into Diagnostics: %v", tc.in, err)
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

	diags, err := validate(&s, []byte(`{}`))
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

	var inv Diagnostics
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

		diag, err := validate(&s, []byte(data))
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
		if d.Code != TooFewItems {
			tb.Fatalf("C: base code %v, want %v", d.Code, TooFewItems)
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
		diag, err := validate(&s, []byte(data))
		if err != nil {
			tb.Fatalf("validate: %v", err)
		}
		if len(diag) != 2 {
			tb.Fatalf("D: diag count %d, want 2", len(diag))
		}

		got := string(Diagnostics(diag).FormatNicelyContext(nil, []byte(data), 5, 5))
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
		got := string(Diag{Off: 0, End: 0, Code: TypeMismatch}.FormatNicelyContext(nil, nil, 5, 5))
		if !strings.Contains(got, "^ Wrong type") {
			tb.Errorf("E empty: %q", got)
		}

		got = string(Diag{Off: 2, End: 100, Code: TooFewItems}.FormatNicelyContext(nil, []byte(`{}`), 5, 5))
		if !strings.Contains(got, "^ Too few items") {
			tb.Errorf("E overrun: %q", got)
		}

		// Large before/after on a short src: caret indent stays small because start
		// clamps to 0, so pad never approaches the 128-wide spaces constant.
		got = string(Diag{Off: 1, End: 2, Code: TooLong}.FormatNicelyContext(nil, []byte(`{}`), 1000, 1000))
		lines := strings.SplitN(got, "\n", 2)
		if indent := strings.IndexByte(lines[1], '^'); indent != 1 {
			tb.Errorf("E wide: caret indent %d, want 1 (stayed within 128): %q", indent, got)
		}
	}
}

func TestDiagOp(tb *testing.T) {
	for _, tc := range []struct {
		schema, data string
		code         DiagCode
		op           Opcode
		value        string
		span         string
	}{
		{`{"type":["integer","null"]}`, `"x"`, TypeMismatch, Type, `["null","integer"]`, `x`},
		{`{"minLength":3}`, `"ab"`, TooShort, MinLen, `3`, `ab`},
		{`{"maxLength":1}`, `"ab"`, TooLong, MaxLen, `1`, `ab`},
		{`{"minimum":3}`, `2`, BelowMinimum, Minimum, `3`, `2`},
		{`{"enum":[1,2]}`, `3`, MustMatchEnum, Enum, `[1,2]`, `3`},
		{`{"const":"a"}`, `"b"`, MustConst, Const, `"a"`, `b`},
		{`{"pattern":"^a+$"}`, `"b"`, PatternMismatch, Pattern, `"^a+$"`, `b`},
		{`{"format":"uuid"}`, `"x"`, FormatMismatch, Format, `"uuid"`, `x`},
		{`{"minItems":2}`, `[1]`, TooFewItems, MinItems, `2`, `[1]`},
		{`{"uniqueItems":true}`, `[1,2,1]`, DuplicateItems, Unique, `true`, `1`},
		{`{"minProperties":1}`, `{}`, TooFewProps, MinProps, `1`, `{}`},
		{`{"not":{"type":"integer"}}`, `5`, MustNotMatch, Not, `{"type":"integer"}`, `5`},
		{`{"anyOf":[{"type":"integer"}]}`, `"x"`, MustMatchAny, AnyOf, `[{"type":"integer"}]`, `x`},
		{`{"oneOf":[{"type":"integer"},{"type":"string"}]}`, `true`, MustMatchOne, OneOf, `[{"type":"integer"},{"type":"string"}]`, `true`},
		{`{"oneOf":[{"type":"integer"},{"type":"number"}]}`, `5`, MustMatchOnlyOne, OneOf, `[{"type":"integer"},{"type":"number"}]`, `5`},
		{`{"properties":{"a":{}},"additionalProperties":false}`, `{"a":1,"zz":2}`, Forbidden, Additional, `false`, `zz`},
		{`{"properties":{"a":false}}`, `{"a":1}`, Forbidden, Properties, `{"a":false}`, `a`},
		{`false`, `5`, Forbidden, Fail, `false`, `5`},
	} {
		s := &Schema{Flags: AssertStringFormat}
		if err := s.Compile([]byte(tc.schema)); err != nil {
			tb.Errorf("compile %s: %v", tc.schema, err)
			continue
		}

		d, err := validate(s, []byte(tc.data))
		if err != nil {
			tb.Errorf("validate %s: %v", tc.data, err)
			continue
		}

		if len(d) != 1 {
			tb.Errorf("validate %s against %s: %d diags, want 1: %+v", tc.data, tc.schema, len(d), d)
			continue
		}

		if d[0].Code != tc.code || d[0].Op.Op() != tc.op {
			tb.Errorf("validate %s against %s: %v on %d, want %v on %d", tc.data, tc.schema, d[0].Code, d[0].Op.Op(), tc.code, tc.op)
		}

		if got := string(s.FormatKeyword(nil, d[0].Op)); got != tc.value {
			tb.Errorf("validate %s against %s: keyword value %s, want %s", tc.data, tc.schema, got, tc.value)
		}

		if got := tc.data[d[0].Off:d[0].End]; got != tc.span {
			tb.Errorf("validate %s against %s: span %q, want %q", tc.data, tc.schema, got, tc.span)
		}
	}
}

func TestDiagDetails(tb *testing.T) {
	s, err := Compile([]byte(`{"type":["integer","null"],"minLength":3,"pattern":"^a+$"}`))
	if err != nil {
		tb.Fatal(err)
	}

	d, err := validate(s, []byte(`"b"`))
	if err != nil || len(d) != 3 {
		tb.Fatalf("diags=%v err=%v, want 3", d, err)
	}

	r := s.Reader()

	for _, x := range d {
		switch x.Code {
		case TypeMismatch:
			if got := TypesOf(x.Op); got != TypeInteger|TypeNull {
				tb.Errorf("type: %v", got)
			}
		case TooShort:
			if x.Op.Imm() != 3 {
				tb.Errorf("minLength: %d", x.Op.Imm())
			}
		case PatternMismatch:
			if got := string(r.String(x.Op)); got != "^a+$" {
				tb.Errorf("pattern: %q", got)
			}
		default:
			tb.Errorf("unexpected %v", x.Code)
		}
	}
}

func TestDiagMissingRequired(tb *testing.T) {
	s, err := Compile([]byte(`{"required":["a","b","c"]}`))
	if err != nil {
		tb.Fatal(err)
	}

	data := []byte(`{"b":1}`)

	d, err := validate(s, data)
	if err != nil {
		tb.Fatal(err)
	}

	if len(d) != 2 {
		tb.Fatalf("diags=%d, want one per missing name: %+v", len(d), d)
	}

	r := s.Reader()

	for i, want := range []string{"a", "c"} {
		if d[i].Code != MissingRequired {
			tb.Errorf("diag %d: %v", i, d[i].Code)
		}

		if got := string(r.String(d[i].Op)); got != want {
			tb.Errorf("diag %d: name %q, want %q", i, got, want)
		}

		if got := string(data[d[i].Off:d[i].End]); got != string(data) {
			tb.Errorf("diag %d: span %q, want the object", i, got)
		}
	}
}

func TestDiagDuplicateItems(tb *testing.T) {
	s, err := Compile([]byte(`{"uniqueItems":true}`))
	if err != nil {
		tb.Fatal(err)
	}

	for _, tc := range []struct {
		data string
		span string
	}{
		{`[1,2,1]`, `1`},
		{`["a","b","b"]`, `b`},
		{`[{"k":1},{"k":2},{"k":1}]`, `{"k":1}`},
	} {
		d, err := validate(s, []byte(tc.data))
		if err != nil || len(d) != 1 {
			tb.Errorf("validate %s: diags=%v err=%v", tc.data, d, err)
			continue
		}

		// the second occurrence, not the array
		if d[0].Off <= strings.Index(tc.data, tc.span) || tc.data[d[0].Off:d[0].End] != tc.span {
			tb.Errorf("validate %s: span %d:%d %q, want the duplicate %q", tc.data, d[0].Off, d[0].End, tc.data[d[0].Off:d[0].End], tc.span)
		}
	}
}
