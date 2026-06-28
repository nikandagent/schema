package schema

import "testing"

func TestValidate(tb *testing.T) {
	for _, tc := range []struct {
		schema, data string
		ok           bool
	}{
		{`{"type":"string"}`, `"x"`, true},
		{`{"type":"string"}`, `1`, false},
		{`{"type":"integer"}`, `5`, true},
		{`{"type":"integer"}`, `5.5`, false},
		{`{"type":"integer"}`, `5.0`, true},
		{`{"type":"number"}`, `5`, true},
		{`{"type":["string","null"]}`, `null`, true},
		{`{"type":["string","null"]}`, `1`, false},

		{`{"minimum":3,"maximum":9}`, `5`, true},
		{`{"minimum":3}`, `2`, false},
		{`{"exclusiveMinimum":3}`, `3`, false},
		{`{"multipleOf":3}`, `9`, true},
		{`{"multipleOf":3}`, `10`, false},
		{`{"minimum":3}`, `"hello"`, true}, // not applicable to strings

		{`{"minLength":2}`, `"ab"`, true},
		{`{"minLength":3}`, `"ab"`, false},

		{`{"properties":{"a":{"type":"integer"}}}`, `{"a":1}`, true},
		{`{"properties":{"a":{"type":"integer"}}}`, `{"a":"x"}`, false},
		{`{"properties":{"a":{"type":"integer"}}}`, `{"b":"x"}`, true},
		{`{"required":["a","b"]}`, `{"a":1}`, false},
		{`{"required":["a"]}`, `{"a":1}`, true},
		{`{"minProperties":2}`, `{"a":1}`, false},

		{`{"items":{"type":"integer"}}`, `[1,2,3]`, true},
		{`{"items":{"type":"integer"}}`, `[1,"x"]`, false},
		{`{"minItems":2}`, `[1]`, false},
		{`{"uniqueItems":true}`, `[1,2,1]`, false},
		{`{"uniqueItems":true}`, `[1,2,"1"]`, true},

		{`{"enum":[1,"x"]}`, `"x"`, true},
		{`{"enum":[1,"x"]}`, `2`, false},
		{`{"enum":[1]}`, `1.0`, true},
		{`{"const":{"a":1}}`, `{"a":1}`, true},
		{`{"const":{"a":1}}`, `{"a":2}`, false},

		{`{"not":{"type":"string"}}`, `1`, true},
		{`{"not":{"type":"string"}}`, `"x"`, false},
		{`{"anyOf":[{"type":"string"},{"type":"integer"}]}`, `1`, true},
		{`{"anyOf":[{"type":"string"},{"type":"boolean"}]}`, `1`, false},
		{`{"oneOf":[{"type":"integer"},{"type":"string"}]}`, `1`, true},
		{`{"oneOf":[{"minimum":1},{"maximum":10}]}`, `5`, false},
		{`{"allOf":[{"type":"integer"},{"minimum":5}]}`, `7`, true},
		{`{"allOf":[{"type":"integer"},{"minimum":5}]}`, `3`, false},

		{`{"type":"object","properties":{"items":{"type":"array","items":{"type":"object","required":["id"]}}}}`,
			`{"items":[{"id":1},{"id":2}]}`, true},
		{`{"type":"object","properties":{"items":{"type":"array","items":{"type":"object","required":["id"]}}}}`,
			`{"items":[{"id":1},{"name":"x"}]}`, false},

		{`{"title":"x","type":"string"}`, `"y"`, true}, // annotation ignored

		{`{"$defs":{"pos":{"type":"integer","minimum":0}},"properties":{"n":{"$ref":"#/$defs/pos"}}}`, `{"n":5}`, true},
		{`{"$defs":{"pos":{"type":"integer","minimum":0}},"properties":{"n":{"$ref":"#/$defs/pos"}}}`, `{"n":-1}`, false},
		{`{"$defs":{"pos":{"type":"integer","minimum":0}},"properties":{"n":{"$ref":"#/$defs/pos"}}}`, `{"n":"x"}`, false},

		{`{"type":"object","properties":{"self":{"$ref":"#"}}}`, `{"self":{"self":{}}}`, true},
		{`{"type":"object","properties":{"self":{"$ref":"#"}}}`, `{"self":5}`, false},

		{`{"$defs":{"node":{"type":"object","properties":{"next":{"$ref":"#/$defs/node"}}}},"$ref":"#/$defs/node"}`, `{"next":{"next":{}}}`, true},
		{`{"$defs":{"node":{"type":"object","properties":{"next":{"$ref":"#/$defs/node"}}}},"$ref":"#/$defs/node"}`, `{"next":5}`, false},
	} {
		s, err := Compile([]byte(tc.schema))
		if err != nil {
			tb.Errorf("compile %q: %v", tc.schema, err)
			continue
		}

		_, err = s.Validate([]byte(tc.data))
		if (err == nil) != tc.ok {
			tb.Errorf("validate %s against %s: ok=%v, err=%v", tc.data, tc.schema, tc.ok, err)
		}
	}
}

func TestRewriteDefault(tb *testing.T) {
	for _, tc := range []struct {
		schema, in, out string
	}{
		{`{"properties":{"a":{"default":1}}}`, `{}`, `{"a":1}`},
		{`{"properties":{"a":{"default":1}}}`, `{"a":5}`, `{"a":5}`}, // present wins
		{`{"properties":{"a":{"default":"x"},"b":{"default":[1,2]}}}`, `{"c":3}`, `{"c":3,"a":"x","b":[1,2]}`},
		{`{"properties":{"a":{"default":{"k":true}}}}`, `{}`, `{"a":{"k":true}}`},
		{`{"properties":{"a":{"type":"integer"}}}`, `{}`, `{}`}, // no default, no insert

		{`{"properties":{"o":{"properties":{"x":{"default":1}}}}}`, `{"o":{}}`, `{"o":{"x":1}}`}, // nested
	} {
		s, err := Compile([]byte(tc.schema))
		if err != nil {
			tb.Errorf("compile %q: %v", tc.schema, err)
			continue
		}

		out, _, err := s.Rewrite(nil, []byte(tc.in))
		if err != nil {
			tb.Errorf("rewrite %s against %s: %v", tc.in, tc.schema, err)
			continue
		}

		if got := string(out); got != tc.out {
			tb.Errorf("rewrite %s against %s: got %q, want %q", tc.in, tc.schema, got, tc.out)
		}
	}
}

func TestRewriteCanonical(tb *testing.T) {
	s, err := Compile([]byte(`{"type":"object"}`))
	if err != nil {
		tb.Fatal(err)
	}

	out, diag, err := s.Rewrite(nil, []byte(`{ "a" : 1 , "b" : [ 2, 3 ] }`))
	if err != nil || len(diag) != 0 {
		tb.Fatalf("err=%v diag=%v", err, diag)
	}

	if got := string(out); got != `{"a":1,"b":[2,3]}` {
		tb.Errorf("rewrite: %q", got)
	}
}
