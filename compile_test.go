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

func TestKeywordTypeErrors(tb *testing.T) {
	for _, tc := range []struct {
		in  string
		msg string
	}{
		{`{"type":"qweqwe"}`, `"type" contains an unknown type name`},
		{`{"type":["object","qweqwe"]}`, `"type" contains an unknown type name`},

		{`{"minimum":"abc"}`, `"minimum" must be a number`},
		{`{"minimum":true}`, `"minimum" must be a number`},
		{`{"maximum":[1]}`, `"maximum" must be a number`},
		{`{"exclusiveMinimum":{}}`, `"exclusiveMinimum" must be a number`},
		{`{"exclusiveMaximum":"x"}`, `"exclusiveMaximum" must be a number`},
		{`{"multipleOf":"x"}`, `"multipleOf" must be a number`},

		{`{"required":[1,2]}`, `"required" entries must be strings`},
		{`{"required":"name"}`, `"required" must be an array`},
		{`{"enum":5}`, `"enum" must be an array`},
		{`{"properties":123}`, `"properties" must be an object`},
		{`{"patternProperties":1}`, `"patternProperties" must be an object`},
		{`{"$defs":1}`, `"$defs" must be an object`},
		{`{"allOf":1}`, `"allOf" must be an array`},
		{`{"anyOf":"x"}`, `"anyOf" must be an array`},
		{`{"oneOf":true}`, `"oneOf" must be an array`},
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

		if !errors.Is(err, ErrKeyword) {
			tb.Errorf("compile %q: err %v, want Is(ErrKeyword)", tc.in, err)
		}
		if e.Message == "" {
			tb.Errorf("compile %q: empty Message", tc.in)
		}
		if e.Message != tc.msg {
			tb.Errorf("compile %q: Message %q, want %q", tc.in, e.Message, tc.msg)
		}
	}
}

func TestKeywordTypeValid(tb *testing.T) {
	for _, in := range []string{
		`{"minimum":0}`,
		`{"maximum":1.5}`,
		`{"exclusiveMinimum":-3}`,
		`{"multipleOf":2}`,
		`{"required":["a","b"]}`,
		`{"enum":[1,"x",true,null]}`,
		`{"properties":{"a":{"type":"string"}}}`,
		`{"allOf":[{"type":"string"}]}`,
		`{"$defs":{"A":{"type":"integer"}}}`,
		`{"const":"anything"}`,
		`{"default":[1,2]}`,
	} {
		var s Schema

		if err := s.Compile([]byte(in)); err != nil {
			tb.Errorf("compile %q: unexpected error: %v", in, err)
		}
	}
}

func TestPath(tb *testing.T) {
	compile := func(src string) *Schema {
		var s Schema
		if err := s.Compile([]byte(src)); err != nil {
			tb.Fatalf("compile %q: %v", src, err)
		}

		return &s
	}

	// 1. Root depth 0: the root's Required node sees empty paths.
	{
		sc := compile(`{"type":"object","required":["a"]}`)

		seen := false
		h := func(c Applier, op, val Opcode) (Opcode, error) {
			if op.Op() == Required {
				seen = true
				if len(c.DataPath()) != 0 || len(c.SchemaPath()) != 0 {
					tb.Errorf("root Required: DataPath=%d SchemaPath=%d, want 0 0",
						len(c.DataPath()), len(c.SchemaPath()))
				}
			}

			return c.Apply(op, val)
		}

		if _, err := sc.Walk([]byte(`{}`), h); err != nil {
			tb.Fatalf("walk: %v", err)
		}
		if !seen {
			tb.Errorf("root Required node never applied")
		}
	}

	// 2. Object key step: one Str step decoding to the property name.
	{
		sc := compile(`{"properties":{"a":{"type":"string"}}}`)

		seen := false
		h := func(c Applier, op, val Opcode) (Opcode, error) {
			if op.Op() == Type {
				seen = true

				dp := c.DataPath()
				if len(dp) != 1 || len(c.SchemaPath()) != 1 {
					tb.Fatalf("prop Type: DataPath=%d SchemaPath=%d, want 1 1",
						len(dp), len(c.SchemaPath()))
				}
				if dp[0].Op() != Str {
					tb.Errorf("prop step Op=%v, want Str", dp[0].Op())
				}
				if got := string(c.Buf().Reader().String(dp[0])); got != "a" {
					tb.Errorf("prop step key %q, want %q", got, "a")
				}
			}

			return c.Apply(op, val)
		}

		if _, err := sc.Walk([]byte(`{"a":"x"}`), h); err != nil {
			tb.Fatalf("walk: %v", err)
		}
		if !seen {
			tb.Errorf("prop Type node never applied")
		}
	}

	// 3. Array index step: one IntLit step, index 0,1,2 across the elements.
	{
		sc := compile(`{"items":{"type":"number"}}`)

		var idx []int
		h := func(c Applier, op, val Opcode) (Opcode, error) {
			if op.Op() == Type {
				dp := c.DataPath()
				if len(dp) != 1 || dp[0].Op() != IntLit {
					tb.Fatalf("item Type: DataPath=%d step=%v, want 1 IntLit",
						len(dp), dp[0].Op())
				}
				idx = append(idx, dp[0].ImmInt())
			}

			return c.Apply(op, val)
		}

		if _, err := sc.Walk([]byte(`[10,20,30]`), h); err != nil {
			tb.Fatalf("walk: %v", err)
		}
		if len(idx) != 3 || idx[0] != 0 || idx[1] != 1 || idx[2] != 2 {
			tb.Errorf("item indices %v, want [0 1 2]", idx)
		}
	}

	// 4. Nested depth + mixed steps: object -> array -> object.
	{
		sc := compile(`{"properties":{"items":{"items":{"properties":{"deep":{"type":"string"}}}}}}`)

		seen := false
		h := func(c Applier, op, val Opcode) (Opcode, error) {
			if op.Op() == Type {
				seen = true

				dp := c.DataPath()
				if len(dp) != 3 {
					tb.Fatalf("deep Type: DataPath=%d, want 3", len(dp))
				}
				if dp[0].Op() != Str || string(c.Buf().Reader().String(dp[0])) != "items" {
					tb.Errorf("step0 %v %q, want Str %q", dp[0].Op(), c.Buf().Reader().String(dp[0]), "items")
				}
				if dp[1].Op() != IntLit || dp[1].ImmInt() != 0 {
					tb.Errorf("step1 %v %d, want IntLit 0", dp[1].Op(), dp[1].ImmInt())
				}
				if dp[2].Op() != Str || string(c.Buf().Reader().String(dp[2])) != "deep" {
					tb.Errorf("step2 %v %q, want Str %q", dp[2].Op(), c.Buf().Reader().String(dp[2]), "deep")
				}
			}

			return c.Apply(op, val)
		}

		if _, err := sc.Walk([]byte(`{"items":[{"deep":"y"}]}`), h); err != nil {
			tb.Fatalf("walk: %v", err)
		}
		if !seen {
			tb.Errorf("deep Type node never applied")
		}
	}

	// 5. allOf does not add depth: the branch's Required stays at depth 0.
	{
		sc := compile(`{"allOf":[{"required":["a"]}]}`)

		seen := false
		h := func(c Applier, op, val Opcode) (Opcode, error) {
			if op.Op() == Required {
				seen = true
				if len(c.DataPath()) != 0 {
					tb.Errorf("allOf Required: DataPath=%d, want 0", len(c.DataPath()))
				}
			}

			return c.Apply(op, val)
		}

		if _, err := sc.Walk([]byte(`{}`), h); err != nil {
			tb.Fatalf("walk: %v", err)
		}
		if !seen {
			tb.Errorf("allOf Required node never applied")
		}
	}

	// 6. Pop correctness: sibling properties are both at depth exactly 1.
	{
		sc := compile(`{"properties":{"a":{"type":"string"},"b":{"type":"string"}}}`)

		var depths []int
		h := func(c Applier, op, val Opcode) (Opcode, error) {
			if op.Op() == Type {
				depths = append(depths, len(c.DataPath()))
			}

			return c.Apply(op, val)
		}

		if _, err := sc.Walk([]byte(`{"a":"x","b":"y"}`), h); err != nil {
			tb.Fatalf("walk: %v", err)
		}
		if len(depths) != 2 || depths[0] != 1 || depths[1] != 1 {
			tb.Errorf("sibling depths %v, want [1 1]", depths)
		}
	}

	// 7. Level-0 skip: suppress the root Required, keep the nested one.
	{
		sc := compile(`{"required":["x"],"properties":{"obj":{"required":["y"]}}}`)

		h := func(c Applier, op, val Opcode) (Opcode, error) {
			if op.Op() == Required && len(c.DataPath()) == 0 {
				return val, nil
			}

			return c.Apply(op, val)
		}

		diag, err := sc.Walk([]byte(`{"obj":{}}`), h)
		if err != nil {
			tb.Fatalf("walk: %v", err)
		}
		if len(diag) != 1 {
			tb.Fatalf("diag count %d, want 1: %+v", len(diag), diag)
		}
		if diag[0].Op != Required {
			tb.Errorf("remaining diag Op=%v, want Required", diag[0].Op)
		}
	}
}

func TestDiagSpan(tb *testing.T) {
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

	// A. Scalar span is half-open and slices the scalar out exactly.
	{
		data := `{"n":123}`
		d := one(`{"properties":{"n":{"type":"string"}}}`, data)

		if got := data[d.Off:d.End]; got != "123" {
			tb.Errorf("scalar span %q [%d,%d), want %q", got, d.Off, d.End, "123")
		}
	}

	// B. Container spans its full source extent [ '[' .. ']'+1 ).
	{
		data := `{"tags":[1]}`
		want := strings.IndexByte(data, '[') // 8
		d := one(`{"properties":{"tags":{"type":"array","minItems":2}}}`, data)

		if d.Off != want {
			tb.Errorf("array Off=%d, want %d", d.Off, want)
		}
		if data[d.Off] != '[' {
			tb.Errorf("array Off points at %q, want '['", data[d.Off])
		}
		if got := data[d.Off:d.End]; got != "[1]" {
			tb.Errorf("array span %q, want %q", got, "[1]")
		}
	}

	// C. Nested object located at the INNER '{', not the root.
	{
		data := `{"o":{"a":1}}`
		want := strings.IndexByte(data, '{') + strings.IndexByte(data[1:], '{') + 1 // 5
		d := one(`{"properties":{"o":{"type":"object","minProperties":3}}}`, data)

		if want != 5 {
			tb.Fatalf("test setup: inner brace index %d, want 5", want)
		}
		if d.Off != want {
			tb.Errorf("object Off=%d, want %d (inner brace)", d.Off, want)
		}
		if data[d.Off] != '{' {
			tb.Errorf("object Off points at %q, want '{'", data[d.Off])
		}
	}

	// D. Root container at offset 0.
	{
		data := `{"a":1}`
		d := one(`{"minProperties":5}`, data)

		if d.Off != 0 || data[d.Off] != '{' {
			tb.Errorf("root Off=%d (%q), want 0 '{'", d.Off, data[d.Off])
		}
	}

	// E. Array element objects located at their own '{' (no owning key).
	{
		var s Schema
		if err := s.Compile([]byte(`{"items":{"type":"object","required":["x"]}}`)); err != nil {
			tb.Fatalf("compile: %v", err)
		}

		data := `[{},{"y":1}]`
		diag, err := s.Validate([]byte(data))
		if err != nil {
			tb.Fatalf("validate: %v", err)
		}
		if len(diag) != 2 {
			tb.Fatalf("diag count %d, want 2: %+v", len(diag), diag)
		}

		for i, want := range []int{1, 4} {
			if diag[i].Off != want {
				tb.Errorf("element %d Off=%d, want %d", i, diag[i].Off, want)
			}
			if data[diag[i].Off] != '{' {
				tb.Errorf("element %d Off points at %q, want '{'", i, data[diag[i].Off])
			}
		}
	}
}

func TestDiagSpanExtra(tb *testing.T) {
	// 1. null/bool type mismatches carry a real literal span.
	{
		var s Schema
		if err := s.Compile([]byte(`{"properties":{"a":{"type":"string"},"b":{"type":"integer"}}}`)); err != nil {
			tb.Fatalf("compile: %v", err)
		}

		data := `{"a":null,"b":true}`
		diag, err := s.Validate([]byte(data))
		if err != nil {
			tb.Fatalf("validate: %v", err)
		}
		if len(diag) != 2 {
			tb.Fatalf("diag count %d, want 2: %+v", len(diag), diag)
		}

		got := map[string]bool{}
		for _, d := range diag {
			if d.End <= d.Off {
				tb.Errorf("literal diag not a real span: [%d,%d)", d.Off, d.End)
			}
			got[data[d.Off:d.End]] = true
		}

		if !got["null"] || !got["true"] {
			tb.Errorf("sliced literals %v, want null and true", got)
		}
	}

	// 2. Container diags span the full extent [Off,End).
	{
		var s Schema
		if err := s.Compile([]byte(`{"properties":{"o":{"type":"object","minProperties":3}},"minProperties":9}`)); err != nil {
			tb.Fatalf("compile: %v", err)
		}

		data := `{"o":{"a":1,"b":2}}`
		diag, err := s.Validate([]byte(data))
		if err != nil {
			tb.Fatalf("validate: %v", err)
		}
		if len(diag) != 2 {
			tb.Fatalf("diag count %d, want 2: %+v", len(diag), diag)
		}

		got := map[string]bool{}
		for _, d := range diag {
			got[data[d.Off:d.End]] = true
		}

		if !got[data] {
			tb.Errorf("root diag does not span whole document; slices %v", got)
		}
		if !got[`{"a":1,"b":2}`] {
			tb.Errorf("inner diag does not span the o object; slices %v", got)
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
