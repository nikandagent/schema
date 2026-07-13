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
		{in: `{"minProperties":1,"maxProperties":3}`},
		{in: `{"properties":{"a":{"type":"integer"}},"required":["a"]}`},
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
		{in: `{"pattern":"^a.*$"}`},
		{in: `{"type":"string","pattern":"^a.*z$"}`},
		{in: `{"default":{"x":1},"type":"object"}`, out: `{"type":"object","default":{"x":1}}`},
		{in: `{"title":"x","description":"y","type":"string"}`},
		{in: `{"x-foo":{"a":[1,2]},"type":"object"}`, out: `{"type":"object","x-foo":{"a":[1,2]}}`},
		{in: `{"$ref":"#"}`},

		{in: `{"x-zeta":1,"x-alpha":2,"type":"string"}`, out: `{"type":"string","x-alpha":2,"x-zeta":1}`},
		{in: `{"format":"email","$comment":"c","type":"string"}`, out: `{"type":"string","$comment":"c","format":"email"}`},
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
