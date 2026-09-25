package schema

import (
	"errors"
	"strings"
	"testing"
)

func TestKeywordTypeErrors(tb *testing.T) {
	for _, tc := range []struct {
		in   string
		code DiagCode
	}{
		{`{"type":"qweqwe"}`, UnknownType},
		{`{"type":["object","qweqwe"]}`, UnknownType},

		{`{"minimum":"abc"}`, MustBeNumber},
		{`{"minimum":true}`, MustBeNumber},
		{`{"maximum":[1]}`, MustBeNumber},
		{`{"exclusiveMinimum":{}}`, MustBeNumber},
		{`{"exclusiveMaximum":"x"}`, MustBeNumber},
		{`{"multipleOf":"x"}`, MustBeNumber},

		{`{"required":[1,2]}`, RequiredNotString},
		{`{"required":"name"}`, MustBeArray},
		{`{"enum":5}`, MustBeArray},
		{`{"properties":123}`, MustBeObject},
		{`{"patternProperties":1}`, MustBeObject},
		{`{"$defs":1}`, MustBeObject},
		{`{"allOf":1}`, MustBeArray},
		{`{"anyOf":"x"}`, MustBeArray},
		{`{"oneOf":true}`, MustBeArray},
	} {
		var s Schema

		err := s.Compile([]byte(tc.in))
		if err == nil {
			tb.Errorf("compile %q: want error", tc.in)
			continue
		}

		d := AsDiag(err)
		if len(d) != 1 {
			tb.Errorf("compile %q: err %v (%T) is not a one-element Diagnostics", tc.in, err, err)
			continue
		}

		if !errors.Is(err, ErrKeyword) {
			tb.Errorf("compile %q: err %v, want Is(ErrKeyword)", tc.in, err)
		}
		if d[0].Code != tc.code {
			tb.Errorf("compile %q: Code %v, want %v", tc.in, d[0].Code, tc.code)
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

	// 1. Root: nothing entered, nothing descended.
	{
		sc := compile(`{"type":"object","required":["a"]}`)

		seen := false
		h := func(c *Applier, s *Schema, op, val Node, h Handler) (Node, error) {
			if op.Op() == Required {
				seen = true

				if c.Depth != 0 || len(c.Steps) != 0 {
					tb.Errorf("root Required: Depth=%d Steps=%d, want 0 0", c.Depth, len(c.Steps))
				}
				if got := string(c.Buffer.Reader().AppendPointer(nil, c.Steps)); got != "." {
					tb.Errorf("root Required: pointer %q, want %q", got, ".")
				}
			}

			return c.Apply(s, op, val, h)
		}

		if _, err := walk(sc, []byte(`{}`), h); err != nil {
			tb.Fatalf("walk: %v", err)
		}
		if !seen {
			tb.Errorf("root Required node never applied")
		}
	}

	// 2. Object key step: entered through Properties, named and keyed by the property.
	{
		sc := compile(`{"properties":{"a":{"type":"string"}}}`)

		seen := false
		h := func(c *Applier, s *Schema, op, val Node, h Handler) (Node, error) {
			if op.Op() == Type {
				seen = true

				if c.Depth != 1 || len(c.Steps) != 1 {
					tb.Fatalf("prop Type: Depth=%d Steps=%d, want 1 1", c.Depth, len(c.Steps))
				}

				r := c.Buffer.Reader()
				st := c.Steps[0]

				if st.Op.Op() != Properties || st.Sub.Op() != All || st.Doc != sc {
					tb.Errorf("prop step: Op=%v Sub=%v samedoc=%v, want Properties All true", st.Op.Op(), st.Sub.Op(), st.Doc == sc)
				}
				if st.DataKey.Op() != String || string(r.String(st.DataKey)) != "a" {
					tb.Errorf("prop step DataKey=%v %q, want String %q", st.DataKey.Op(), r.String(st.DataKey), "a")
				}
				if got := string(r.AppendPointer(nil, c.Steps)); got != ".a" {
					tb.Errorf("prop pointer %q, want %q", got, ".a")
				}
			}

			return c.Apply(s, op, val, h)
		}

		if _, err := walk(sc, []byte(`{"a":"x"}`), h); err != nil {
			tb.Fatalf("walk: %v", err)
		}
		if !seen {
			tb.Errorf("prop Type node never applied")
		}
	}

	// 3. Array index step: one IntLit key, index 0,1,2 across the elements.
	{
		sc := compile(`{"items":{"type":"number"}}`)

		var ptr []string
		h := func(c *Applier, s *Schema, op, val Node, h Handler) (Node, error) {
			if op.Op() == Type {
				if c.Depth != 1 || c.Steps[0].DataKey.Op() != IntLit {
					tb.Fatalf("item Type: Depth=%d key=%v, want 1 IntLit", c.Depth, c.Steps[0].DataKey.Op())
				}

				ptr = append(ptr, string(c.Buffer.Reader().AppendPointer(nil, c.Steps)))
			}

			return c.Apply(s, op, val, h)
		}

		if _, err := walk(sc, []byte(`[10,20,30]`), h); err != nil {
			tb.Fatalf("walk: %v", err)
		}
		if len(ptr) != 3 || ptr[0] != "[0]" || ptr[1] != "[1]" || ptr[2] != "[2]" {
			tb.Errorf("item pointers %v, want [0] [1] [2]", ptr)
		}
	}

	// 4. Nested depth + mixed steps: object -> array -> object.
	{
		sc := compile(`{"properties":{"items":{"items":{"properties":{"deep":{"type":"string"}}}}}}`)

		seen := false
		h := func(c *Applier, s *Schema, op, val Node, h Handler) (Node, error) {
			if op.Op() == Type {
				seen = true

				if c.Depth != 3 {
					tb.Fatalf("deep Type: Depth=%d, want 3", c.Depth)
				}
				if got := string(c.Buffer.Reader().AppendPointer(nil, c.Steps)); got != ".items[0].deep" {
					tb.Errorf("deep pointer %q, want %q", got, ".items[0].deep")
				}
			}

			return c.Apply(s, op, val, h)
		}

		if _, err := walk(sc, []byte(`{"items":[{"deep":"y"}]}`), h); err != nil {
			tb.Fatalf("walk: %v", err)
		}
		if !seen {
			tb.Errorf("deep Type node never applied")
		}
	}

	// 5. allOf is a step the data does not follow: Steps grows, Depth does not.
	{
		sc := compile(`{"allOf":[{"required":["a"]}]}`)

		seen := false
		h := func(c *Applier, s *Schema, op, val Node, h Handler) (Node, error) {
			if op.Op() == Required {
				seen = true

				if c.Depth != 0 || len(c.Steps) != 1 {
					tb.Errorf("allOf Required: Depth=%d Steps=%d, want 0 1", c.Depth, len(c.Steps))
				}
				if st := c.Steps[0]; st.Op.Op() != AllOf || st.DataKey.Op() != None {
					tb.Errorf("allOf step: Op=%v DataKey=%v, want AllOf None", st.Op.Op(), st.DataKey)
				}
				if got := string(c.Buffer.Reader().AppendPointer(nil, c.Steps)); got != "." {
					tb.Errorf("allOf pointer %q, want %q", got, ".")
				}
			}

			return c.Apply(s, op, val, h)
		}

		if _, err := walk(sc, []byte(`{}`), h); err != nil {
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
		h := func(c *Applier, s *Schema, op, val Node, h Handler) (Node, error) {
			if op.Op() == Type {
				depths = append(depths, c.Depth)
			}

			return c.Apply(s, op, val, h)
		}

		if _, err := walk(sc, []byte(`{"a":"x","b":"y"}`), h); err != nil {
			tb.Fatalf("walk: %v", err)
		}
		if len(depths) != 2 || depths[0] != 1 || depths[1] != 1 {
			tb.Errorf("sibling depths %v, want [1 1]", depths)
		}
	}

	// 7. Level-0 skip: suppress the root Required, keep the nested one.
	{
		sc := compile(`{"required":["x"],"properties":{"obj":{"required":["y"]}}}`)

		h := func(c *Applier, s *Schema, op, val Node, h Handler) (Node, error) {
			if op.Op() == Required && c.Depth == 0 {
				return val, nil
			}

			return c.Apply(s, op, val, h)
		}

		diag, err := walk(sc, []byte(`{"obj":{}}`), h)
		if err != nil {
			tb.Fatalf("walk: %v", err)
		}
		if len(diag) != 1 {
			tb.Fatalf("diag count %d, want 1: %+v", len(diag), diag)
		}
		if diag[0].Code != MissingRequired {
			tb.Errorf("remaining diag Code=%v, want %v", diag[0].Code, MissingRequired)
		}
	}
}

func TestDiagSpan(tb *testing.T) {
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

	// A. Scalar span is half-open and slices the scalar out exactly.
	{
		data := `{"n":123}`
		d := one(`{"properties":{"n":{"type":"string"}}}`, data)

		off, end := d.valSpan()
		if got := data[off:end]; got != "123" {
			tb.Errorf("scalar span %q [%d,%d), want %q", got, off, end, "123")
		}
	}

	// B. Container spans its full source extent [ '[' .. ']'+1 ).
	{
		data := `{"tags":[1]}`
		want := strings.IndexByte(data, '[') // 8
		d := one(`{"properties":{"tags":{"type":"array","minItems":2}}}`, data)

		off, end := d.valSpan()
		if off != want {
			tb.Errorf("array off=%d, want %d", off, want)
		}
		if data[off] != '[' {
			tb.Errorf("array off points at %q, want '['", data[off])
		}
		if got := data[off:end]; got != "[1]" {
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
		off, _ := d.valSpan()
		if off != want {
			tb.Errorf("object off=%d, want %d (inner brace)", off, want)
		}
		if data[off] != '{' {
			tb.Errorf("object off points at %q, want '{'", data[off])
		}
	}

	// D. Root container at offset 0.
	{
		data := `{"a":1}`
		d := one(`{"minProperties":5}`, data)

		off, _ := d.valSpan()
		if off != 0 || data[off] != '{' {
			tb.Errorf("root off=%d (%q), want 0 '{'", off, data[off])
		}
	}

	// E. Array element objects located at their own '{' (no owning key).
	{
		var s Schema
		if err := s.Compile([]byte(`{"items":{"type":"object","required":["x"]}}`)); err != nil {
			tb.Fatalf("compile: %v", err)
		}

		data := `[{},{"y":1}]`
		diag, err := validate(&s, []byte(data))
		if err != nil {
			tb.Fatalf("validate: %v", err)
		}
		if len(diag) != 2 {
			tb.Fatalf("diag count %d, want 2: %+v", len(diag), diag)
		}

		for i, want := range []int{1, 4} {
			off, _ := diag[i].valSpan()

			if off != want {
				tb.Errorf("element %d off=%d, want %d", i, off, want)
			}
			if data[off] != '{' {
				tb.Errorf("element %d off points at %q, want '{'", i, data[off])
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
		diag, err := validate(&s, []byte(data))
		if err != nil {
			tb.Fatalf("validate: %v", err)
		}
		if len(diag) != 2 {
			tb.Fatalf("diag count %d, want 2: %+v", len(diag), diag)
		}

		got := map[string]bool{}
		for _, d := range diag {
			off, end := d.valSpan()

			if end <= off {
				tb.Errorf("literal diag not a real span: [%d,%d)", off, end)
			}

			got[data[off:end]] = true
		}

		if !got["null"] || !got["true"] {
			tb.Errorf("sliced literals %v, want null and true", got)
		}
	}

	// 2. Container diags span the full extent.
	{
		var s Schema
		if err := s.Compile([]byte(`{"properties":{"o":{"type":"object","minProperties":3}},"minProperties":9}`)); err != nil {
			tb.Fatalf("compile: %v", err)
		}

		data := `{"o":{"a":1,"b":2}}`
		diag, err := validate(&s, []byte(data))
		if err != nil {
			tb.Fatalf("validate: %v", err)
		}
		if len(diag) != 2 {
			tb.Fatalf("diag count %d, want 2: %+v", len(diag), diag)
		}

		got := map[string]bool{}
		for _, d := range diag {
			off, end := d.valSpan()
			got[data[off:end]] = true
		}

		if !got[data] {
			tb.Errorf("root diag does not span whole document; slices %v", got)
		}
		if !got[`{"a":1,"b":2}`] {
			tb.Errorf("inner diag does not span the o object; slices %v", got)
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
		{`{"contains":{"type":"string"}}`, SchemaRejectUnknown, true},      // recognized, kept for round-trip
		{`{"contains":{"type":"string"}}`, SchemaRejectUnsupported, false}, // recognized-but-unimplemented, rejected
		{`{"nope":1}`, SchemaRejectUnsupported, true},                      // genuine unknown, not a target of this flag
	} {
		s := Schema{Flags: tc.flags}

		err := s.Compile([]byte(tc.in))
		if (err == nil) != tc.ok {
			tb.Errorf("compile %q flags=%b: ok=%v, err=%v", tc.in, tc.flags, tc.ok, err)
		}
	}
}
