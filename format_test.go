package schema

import "testing"

func TestFormat(tb *testing.T) {
	for _, tc := range []struct {
		in, out string
	}{
		{in: `true`},
		{in: `false`},
		{in: `{}`},
		{in: `{"type":"string"}`},
		{in: `{"type":["null","string"]}`},
		{in: `{"type":["string","null"]}`, out: `{"type":["null","string"]}`},
		{in: `{"minimum":1,"maximum":9,"multipleOf":3}`},
		{in: `{"minLength":2,"maxLength":5}`},
		{in: `{"minItems":0,"maxItems":10,"uniqueItems":true}`},
		{in: `{"uniqueItems":false}`}, // inert, but preserved: compile never discards
		{in: `{"minProperties":1,"maxProperties":3}`},
		{in: `{"properties":{"a":{"type":"integer"}},"required":["a"]}`},
		// required is reordered to follow properties, and an escaped name is the
		// same name — it sorts into its property's slot and formats decoded.
		{
			in:  `{"properties":{"a":{},"b":{}},"required":["b","\u0061"]}`,
			out: `{"properties":{"a":{},"b":{}},"required":["a","b"]}`,
		},
		{in: `{"enum":[1,"x",null,true]}`},
		{in: `{"const":{"a":[1,2]}}`},
		{in: `{"items":{"type":"number"}}`},
		{in: `{"additionalProperties":false}`},
		{in: `{"additionalProperties":{"type":"string"}}`},
		{in: `{"properties":{"a":{"type":"integer"}},"additionalProperties":false}`},
		{in: `{"properties":{"a":{"type":"integer"}},"additionalProperties":{"type":"string"}}`},
		{in: `{"patternProperties":{"^a":{"type":"number"}}}`},
		{in: `{"properties":{"a":{"type":"integer"}},"patternProperties":{"^x":{"type":"string"}},"additionalProperties":false}`},
		{in: `{"allOf":[{"type":"object"},{"not":{"type":"string"}}]}`},
		{in: `{"anyOf":[{"type":"string"},{"type":"integer"}]}`},
		{in: `{"if":{"type":"string"},"then":{"minLength":2},"else":{"type":"integer"}}`},
		{in: `{"if":{"type":"string"},"then":{"minLength":2}}`},
		{in: `{"if":{"type":"string"}}`},
		{in: `{"then":{"type":"string"},"else":{"type":"integer"}}`},
		{in: `{"else":{"type":"integer"},"then":{"minLength":2},"if":{"type":"string"}}`,
			out: `{"if":{"type":"string"},"then":{"minLength":2},"else":{"type":"integer"}}`},
		{in: `{"default":1,"else":{"type":"integer"},"if":{"type":"string"},"allOf":[{"type":"number"}],"type":"number"}`,
			out: `{"type":"number","allOf":[{"type":"number"}],"if":{"type":"string"},"else":{"type":"integer"},"default":1}`},
		{in: `{"properties":{"a":{"if":{"type":"string"},"then":{"minLength":2}}}}`},

		{in: `{"prefixItems":[{"type":"integer"},{"type":"string"}]}`},
		{in: `{"prefixItems":[true,false]}`},
		{in: `{"prefixItems":[{"type":"integer"}],"items":{"type":"string"}}`},
		{in: `{"prefixItems":[{"type":"integer"}],"items":false}`},
		{in: `{"items":{"type":"string"},"prefixItems":[{"type":"integer"}]}`,
			out: `{"prefixItems":[{"type":"integer"}],"items":{"type":"string"}}`},
		{in: `{"items":{"type":"string"},"maxItems":3,"prefixItems":[{"type":"integer"}],"minItems":1}`,
			out: `{"minItems":1,"maxItems":3,"prefixItems":[{"type":"integer"}],"items":{"type":"string"}}`},
		{in: `{"properties":{"a":{"prefixItems":[{"prefixItems":[{"type":"integer"}],"items":false}],"items":{"type":"string"}}}}`},

		{in: `{"pattern":"^a.*$"}`},
		{in: `{"type":"string","pattern":"^a.*z$"}`},
		{in: `{"default":{"x":1},"type":"object"}`, out: `{"type":"object","default":{"x":1}}`},
		{in: `{"title":"x","description":"y","type":"string"}`},
		{in: `{"x-foo":{"a":[1,2]},"type":"object"}`, out: `{"type":"object","x-foo":{"a":[1,2]}}`},
		{in: `{"$ref":"#"}`},

		{in: `{"x-zeta":1,"x-alpha":2,"type":"string"}`, out: `{"type":"string","x-alpha":2,"x-zeta":1}`},
		{in: `{"format":"email","$comment":"c","type":"string"}`, out: `{"type":"string","format":"email","$comment":"c"}`},
		{in: `{"format":"email","x-foo":1,"description":"d","title":"t","type":"string"}`,
			out: `{"title":"t","description":"d","type":"string","x-foo":1,"format":"email"}`},

		{in: `{"properties":{"a":{"type":"integer"},"b":{"type":"string"}},"required":["b","a"]}`,
			out: `{"properties":{"a":{"type":"integer"},"b":{"type":"string"}},"required":["a","b"]}`},

		{in: `{"$defs":{"x":{"type":"integer"}},"properties":{"n":{"$ref":"#/$defs/x"}}}`,
			out: `{"properties":{"n":{"$ref":"#/$defs/x"}},"$defs":{"x":{"type":"integer"}}}`},
		{in: `{"definitions":{"x":{"type":"integer"}},"$ref":"#/definitions/x"}`,
			out: `{"$ref":"#/$defs/x","$defs":{"x":{"type":"integer"}}}`},

		// JSON Pointer escaped $defs keys: stored escaped, re-emitted original.
		{in: `{"$defs":{"a/b":{"type":"integer"}},"properties":{"n":{"$ref":"#/$defs/a~1b"}}}`,
			out: `{"properties":{"n":{"$ref":"#/$defs/a~1b"}},"$defs":{"a/b":{"type":"integer"}}}`},
		{in: `{"$defs":{"x~y":{"type":"integer"}},"properties":{"n":{"$ref":"#/$defs/x~0y"}}}`,
			out: `{"properties":{"n":{"$ref":"#/$defs/x~0y"}},"$defs":{"x~y":{"type":"integer"}}}`},
		{in: `{"$defs":{"a/b~c":{"type":"integer"}},"properties":{"n":{"$ref":"#/$defs/a~1b~0c"}}}`,
			out: `{"properties":{"n":{"$ref":"#/$defs/a~1b~0c"}},"$defs":{"a/b~c":{"type":"integer"}}}`},
	} {
		want := tc.out
		if want == "" {
			want = tc.in
		}

		var s Schema

		err := s.Compile([]byte(tc.in))
		if err != nil {
			tb.Errorf("compile %q: %v", tc.in, err)
			continue
		}

		got := string(s.Format(nil))
		if got != want {
			tb.Errorf("format %q: got %q, want %q", tc.in, got, want)
		}
	}
}

func TestFormatEscapes(tb *testing.T) {
	for _, tc := range []struct {
		in, out string
	}{
		{in: `{"title":"caf\u00e9"}`, out: `{"title":"café"}`},
		{in: `{"properties":{"caf\u00e9":{"pattern":"^\u0061+$"}},"required":["caf\u00e9"]}`,
			out: `{"properties":{"café":{"pattern":"^a+$"}},"required":["café"]}`},
		{in: `{"$defs":{"caf\u00e9":{"type":"integer"}},"$ref":"#/$defs/caf\u00e9"}`,
			out: `{"$ref":"#/$defs/café","$defs":{"café":{"type":"integer"}}}`},
		{in: `{"enum":["caf\u00e9","\u0061"]}`, out: `{"enum":["café","a"]}`},

		{in: `{"title":"a\"b"}`},
		{in: `{"title":"a\\b"}`},
		{in: `{"title":"a\nb"}`},
		{in: `{"title":"a\u0007b"}`},
		{in: `{"const":"café"}`},
	} {
		want := tc.out
		if want == "" {
			want = tc.in
		}

		s, err := Compile([]byte(tc.in))
		if err != nil {
			tb.Errorf("compile %q: %v", tc.in, err)
			continue
		}

		got := string(s.Format(nil))
		if got != want {
			tb.Errorf("format %q: got %q, want %q", tc.in, got, want)
			continue
		}

		var t Schema
		if err := t.Compile([]byte(got)); err != nil {
			tb.Errorf("recompile %q: %v", got, err)
			continue
		}

		if again := string(t.Format(nil)); again != got {
			tb.Errorf("format twice %q: got %q, want %q", tc.in, again, got)
		}
	}
}

func TestFormatKeepOrder(tb *testing.T) {
	// SchemaKeepOrder preserves authored keyword and required order.
	for _, in := range []string{
		`{"default":{"x":1},"type":"object"}`,
		`{"title":"x","description":"y","type":"string"}`,
		`{"properties":{"a":{"type":"integer"},"b":{"type":"string"}},"required":["b","a"]}`,
	} {
		s := Schema{Flags: SchemaKeepOrder}

		err := s.Compile([]byte(in))
		if err != nil {
			tb.Errorf("compile %q: %v", in, err)
			continue
		}

		if got := string(s.Format(nil)); got != in {
			tb.Errorf("format %q: got %q, want %q", in, got, in)
		}
	}
}

func TestFormatKeyword(tb *testing.T) {
	s, err := Compile([]byte(`{
		"type":["integer","null"],"properties":{"a":{"type":"string"}},"$defs":{"T":{}},"patternProperties":{"^x":{}},
		"required":["a","b"],"enum":[1,"x"],"const":{"k":1},"default":[1],
		"minimum":1,"maximum":2.5,"exclusiveMinimum":0,"exclusiveMaximum":3,"multipleOf":2,
		"items":{"type":"number"},"prefixItems":[{"type":"string"}],"additionalProperties":false,"not":{"type":"null"},
		"if":{"type":"string"},"then":{"minLength":1},"else":{"maxLength":9},"allOf":[{}],"anyOf":[{}],"oneOf":[{}],
		"minLength":1,"maxLength":2,"minItems":3,"maxItems":4,"minProperties":5,"maxProperties":6,"uniqueItems":true,
		"pattern":"^a$","format":"uuid","$ref":"#/$defs/T","title":"t","x-ext":{"q":[1]}
	}`))
	if err != nil {
		tb.Fatal(err)
	}

	r := s.Reader()

	want := map[string]string{
		"type":                 `["null","integer"]`,
		"properties":           `{"a":{"type":"string"}}`,
		"$defs":                `{"T":{}}`,
		"patternProperties":    `{"^x":{}}`,
		"required":             `["a","b"]`,
		"enum":                 `[1,"x"]`,
		"const":                `{"k":1}`,
		"default":              `[1]`,
		"minimum":              `1`,
		"maximum":              `2.5`,
		"exclusiveMinimum":     `0`,
		"exclusiveMaximum":     `3`,
		"multipleOf":           `2`,
		"items":                `{"type":"number"}`,
		"prefixItems":          `[{"type":"string"}]`,
		"additionalProperties": `false`,
		"not":                  `{"type":"null"}`,
		"if":                   `{"type":"string"}`,
		"then":                 `{"minLength":1}`,
		"else":                 `{"maxLength":9}`,
		"allOf":                `[{}]`,
		"anyOf":                `[{}]`,
		"oneOf":                `[{}]`,
		"minLength":            `1`,
		"maxLength":            `2`,
		"minItems":             `3`,
		"maxItems":             `4`,
		"minProperties":        `5`,
		"maxProperties":        `6`,
		"uniqueItems":          `true`,
		"pattern":              `"^a$"`,
		"format":               `"uuid"`,
		"$ref":                 `"#/$defs/T"`,
	}

	seen := map[string]bool{}

	for _, op := range r.Nodes(s.Root()) {
		if op.Op() == Raw || op.Op() == Ext {
			continue
		}

		name := op.Keyword()
		seen[name] = true

		if got := string(s.FormatKeyword(nil, op)); got != want[name] {
			tb.Errorf("%s: got %s, want %s", name, got, want[name])
		}
	}

	for name := range want {
		if !seen[name] {
			tb.Errorf("%s: never seen", name)
		}
	}

	if got := string(s.FormatKeyword(nil, r.Keyword(s.Root(), Raw))); got != `"t"` {
		tb.Errorf("title: got %s", got)
	}

	if got := string(s.FormatKeyword(nil, r.Keyword(s.Root(), Ext))); got != `{"q":[1]}` {
		tb.Errorf("x-ext: got %s", got)
	}

	if got := string(s.FormatKeyword(nil, Node{op: Fail})); got != `false` {
		tb.Errorf("Fail: got %s", got)
	}
}

func TestKeywordEntry(tb *testing.T) {
	s, err := Compile([]byte(`{"required":["a"]}`))
	if err != nil {
		tb.Fatal(err)
	}

	r := s.Reader()
	_, entry := r.NodesAt(r.Keyword(s.Root(), Required), 0)

	func() {
		defer func() {
			if p := recover(); p != nil {
				tb.Errorf("FormatKeyword(required entry): panic %v, want the name", p)
			}
		}()

		if got := string(s.FormatKeyword(nil, entry)); got != `"a"` {
			tb.Errorf("FormatKeyword(required entry): got %s, want %q", got, `"a"`)
		}
	}()

	for _, tc := range []struct {
		name string
		op   Node
	}{
		{"required entry", entry},
		{"Pass", Node{op: Pass}},
		{"Fail", Node{op: Fail}},
		{"None", Node{}},
	} {
		func() {
			defer func() {
				if p := recover(); p != nil {
					tb.Errorf("Keyword(%s): panic %v, want \"\"", tc.name, p)
				}
			}()

			if got := tc.op.Keyword(); got != "" {
				tb.Errorf("Keyword(%s): got %q, want \"\"", tc.name, got)
			}
		}()
	}
}

// TestFormatUncompiled pins the documented panic: Format reads the compiled
// program, so there must be one.
func TestFormatUncompiled(tb *testing.T) {
	var zero Schema

	mustPanic(tb, "Format(zero Schema)", func() { zero.Format(nil) })

	var failed Schema

	if err := failed.Compile([]byte(`{"minLength":"x"}`)); err == nil {
		tb.Fatalf("compile: want error")
	}

	mustPanic(tb, "Format(failed Compile)", func() { failed.Format(nil) })

	// a compiled one is fine, and stays fine after a failed recompile attempt
	ok, err := Compile([]byte(`{"type":"string"}`))
	if err != nil {
		tb.Fatal(err)
	}

	if got := string(ok.Format(nil)); got != `{"type":"string"}` {
		tb.Errorf("format: got %s", got)
	}
}
