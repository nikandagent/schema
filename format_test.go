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
		{in: `{"allOf":[{"type":"object"},{"not":{"type":"string"}}]}`},
		{in: `{"anyOf":[{"type":"string"},{"type":"integer"}]}`},
		{in: `{"pattern":"^a.*$"}`},
		{in: `{"default":{"x":1},"type":"object"}`, out: `{"type":"object","default":{"x":1}}`},
		{in: `{"title":"x","description":"y","type":"string"}`, out: `{"type":"string","title":"x","description":"y"}`},
		{in: `{"x-foo":{"a":[1,2]},"type":"object"}`, out: `{"type":"object","x-foo":{"a":[1,2]}}`},
		{in: `{"$ref":"#"}`},

		{in: `{"properties":{"a":{"type":"integer"},"b":{"type":"string"}},"required":["b","a"]}`,
			out: `{"properties":{"a":{"type":"integer"},"b":{"type":"string"}},"required":["a","b"]}`},

		{in: `{"$defs":{"x":{"type":"integer"}},"properties":{"n":{"$ref":"#/$defs/x"}}}`,
			out: `{"properties":{"n":{"$ref":"#/$defs/x"}},"$defs":{"x":{"type":"integer"}}}`},
		{in: `{"definitions":{"x":{"type":"integer"}},"$ref":"#/definitions/x"}`,
			out: `{"$ref":"#/$defs/x","$defs":{"x":{"type":"integer"}}}`},
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
