package schema

import (
	"bytes"
	"errors"
	"sort"
	"strconv"
	"strings"
	"testing"
)

const diagHandlerSaysNo = UserDiagBase + iota

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
		{`{"uniqueItems":false}`, `[1,2,1]`, true},

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

		{`{"if":{"type":"string"},"then":{"minLength":2}}`, `"ab"`, true},
		{`{"if":{"type":"string"},"then":{"minLength":2}}`, `"a"`, false},
		{`{"if":{"type":"string"},"then":{"minLength":2}}`, `1`, true}, // condition failing is not itself a diag
		{`{"if":{"type":"string"},"else":{"minimum":3}}`, `"a"`, true},
		{`{"if":{"type":"string"},"else":{"minimum":3}}`, `5`, true},
		{`{"if":{"type":"string"},"else":{"minimum":3}}`, `1`, false},
		{`{"if":{"minimum":10},"then":{"multipleOf":5},"else":{"multipleOf":2}}`, `15`, true},
		{`{"if":{"minimum":10},"then":{"multipleOf":5},"else":{"multipleOf":2}}`, `11`, false},
		{`{"if":{"minimum":10},"then":{"multipleOf":5},"else":{"multipleOf":2}}`, `4`, true},
		{`{"if":{"minimum":10},"then":{"multipleOf":5},"else":{"multipleOf":2}}`, `3`, false},

		{`{"then":{"type":"string"}}`, `1`, true}, // no if: branch ignored
		{`{"else":{"type":"string"}}`, `1`, true},
		{`{"then":{"type":"string"},"else":{"type":"boolean"}}`, `1`, true},
		{`{"if":{"type":"string"}}`, `1`, true}, // no branches: condition is inert
		{`{"if":{"type":"string"}}`, `"x"`, true},

		{`{"properties":{"a":{"if":{"type":"string"},"then":{"minLength":2}}}}`, `{"a":"xy"}`, true},
		{`{"properties":{"a":{"if":{"type":"string"},"then":{"minLength":2}}}}`, `{"a":"x"}`, false},
		{`{"properties":{"a":{"if":{"type":"string"},"then":{"minLength":2}}}}`, `{"a":1}`, true},
		{`{"allOf":[{"if":{"type":"integer"},"then":{"minimum":5}},{"if":{"type":"integer"},"else":{"type":"string"}}]}`, `7`, true},
		{`{"allOf":[{"if":{"type":"integer"},"then":{"minimum":5}},{"if":{"type":"integer"},"else":{"type":"string"}}]}`, `3`, false},
		{`{"allOf":[{"if":{"type":"integer"},"then":{"minimum":5}},{"if":{"type":"integer"},"else":{"type":"string"}}]}`, `true`, false},
		{`{"allOf":[{"if":{"type":"integer"},"then":{"minimum":5}},{"if":{"type":"integer"},"else":{"type":"string"}}]}`, `"x"`, true},

		{`{"if":{"properties":{"k":{"const":"a"}},"required":["k"]},"then":{"required":["av"]},"else":{"required":["bv"]}}`, `{"k":"a","av":1}`, true},
		{`{"if":{"properties":{"k":{"const":"a"}},"required":["k"]},"then":{"required":["av"]},"else":{"required":["bv"]}}`, `{"k":"a","bv":1}`, false},
		{`{"if":{"properties":{"k":{"const":"a"}},"required":["k"]},"then":{"required":["av"]},"else":{"required":["bv"]}}`, `{"k":"b","bv":1}`, true},

		{`{"prefixItems":[{"type":"integer"},{"type":"string"}]}`, `[1,"x"]`, true},
		{`{"prefixItems":[{"type":"integer"},{"type":"string"}]}`, `["x",1]`, false},
		{`{"prefixItems":[{"type":"integer"},{"type":"string"}]}`, `[1]`, true}, // shorter than the tuple
		{`{"prefixItems":[{"type":"integer"}]}`, `[1,"x",null]`, true},          // tail unconstrained
		{`{"prefixItems":[{"type":"integer"}]}`, `[]`, true},
		{`{"prefixItems":[{"type":"integer"}]}`, `{"0":"x"}`, true}, // not an array
		{`{"prefixItems":[true,false]}`, `[1]`, true},
		{`{"prefixItems":[true,false]}`, `[1,2]`, false},
		{`{"prefixItems":[{"type":"integer"}],"items":{"type":"string"}}`, `[1,"a","b"]`, true},
		{`{"prefixItems":[{"type":"integer"}],"items":{"type":"string"}}`, `[1,"a",2]`, false},
		{`{"prefixItems":[{"type":"integer"}],"items":{"type":"string"}}`, `["a","a"]`, false},
		{`{"items":{"type":"string"},"prefixItems":[{"type":"integer"}]}`, `[1,"a"]`, true},
		{`{"prefixItems":[{"type":"integer"}],"items":false}`, `[1]`, true},
		{`{"prefixItems":[{"type":"integer"}],"items":false}`, `[1,2]`, false},
		{`{"prefixItems":[{"type":"integer"},{"type":"integer"}],"minItems":2}`, `[1]`, false},
		{`{"prefixItems":[{"type":"integer"},{"type":"integer"}],"maxItems":1}`, `[1,2]`, false},

		{`{"properties":{"a":{"prefixItems":[{"type":"integer"}]}}}`, `{"a":[1,"x"]}`, true},
		{`{"properties":{"a":{"prefixItems":[{"type":"integer"}]}}}`, `{"a":["x"]}`, false},
		{`{"prefixItems":[{"prefixItems":[{"type":"integer"}]}]}`, `[[1,"x"],"y"]`, true},
		{`{"prefixItems":[{"prefixItems":[{"type":"integer"}]}]}`, `[["x"],"y"]`, false},
		{`{"if":{"prefixItems":[{"const":"a"}]},"then":{"minItems":2}}`, `["a",1]`, true},
		{`{"if":{"prefixItems":[{"const":"a"}]},"then":{"minItems":2}}`, `["a"]`, false},
		{`{"if":{"prefixItems":[{"const":"a"}]},"then":{"minItems":2}}`, `["b"]`, true},

		{
			`{"type":"object","properties":{"items":{"type":"array","items":{"type":"object","required":["id"]}}}}`,
			`{"items":[{"id":1},{"id":2}]}`, true,
		},
		{
			`{"type":"object","properties":{"items":{"type":"array","items":{"type":"object","required":["id"]}}}}`,
			`{"items":[{"id":1},{"name":"x"}]}`, false,
		},

		{`{"title":"x","type":"string"}`, `"y"`, true}, // annotation ignored

		// One string, many spellings: equality is on the value, not the encoding.
		{`{"const":"a"}`, `"\u0061"`, true},
		{`{"const":"\u0061"}`, `"a"`, true},
		{`{"const":"a"}`, `"b"`, false},
		{`{"const":"\u0061"}`, `"b"`, false},
		{`{"enum":["a","b"]}`, `"\u0062"`, true},
		{`{"enum":["a"]}`, `"\u0062"`, false},
		{`{"const":"\n"}`, "\"\\u000a\"", true},
		{`{"const":"\ud83d\ude00"}`, `"\uD83D\uDE00"`, true},
		{`{"const":"a"}`, `"ab"`, false},
		{`{"const":"\u0061"}`, `"ab"`, false},
		{`{"const":"ab"}`, `"\u0061"`, false},
		{`{"required":["a"]}`, `{"\u0061":1}`, true},
		{`{"required":["\u0061"]}`, `{"a":1}`, true},
		{`{"required":["a"]}`, `{"b":1}`, false},
		{`{"properties":{"a":{"type":"integer"}}}`, `{"\u0061":"x"}`, false},
		{`{"properties":{"\u0061":{"type":"integer"}}}`, `{"a":"x"}`, false},
		{`{"properties":{"a":{"type":"integer"}}}`, `{"\u0061":1}`, true},
		{`{"uniqueItems":true}`, `["a","\u0061"]`, false},
		{`{"uniqueItems":true}`, `["a","\u0062"]`, true},
		{`{"additionalProperties":false,"properties":{"a":{}}}`, `{"\u0061":1}`, true},

		// $defs names and $anchor fragments are stored decoded, so an escaped $ref
		// must match them by value too.
		{`{"$defs":{"a":{"type":"integer"}},"$ref":"#/$defs/\u0061"}`, `1`, true},
		{`{"$defs":{"a":{"type":"integer"}},"$ref":"#/$defs/\u0061"}`, `"x"`, false},
		{`{"$defs":{"\u0061":{"type":"integer"}},"$ref":"#/$defs/a"}`, `"x"`, false},
		{`{"$defs":{"a":{"type":"integer"}},"$anchor":"\u0061"}`, `1`, true},
		{`{"$defs":{"x":{"$anchor":"a","type":"integer"}},"$ref":"#\u0061"}`, `"x"`, false},

		{`{"$defs":{"pos":{"type":"integer","minimum":0}},"properties":{"n":{"$ref":"#/$defs/pos"}}}`, `{"n":5}`, true},
		{`{"$defs":{"pos":{"type":"integer","minimum":0}},"properties":{"n":{"$ref":"#/$defs/pos"}}}`, `{"n":-1}`, false},
		{`{"$defs":{"pos":{"type":"integer","minimum":0}},"properties":{"n":{"$ref":"#/$defs/pos"}}}`, `{"n":"x"}`, false},

		{`{"type":"object","properties":{"self":{"$ref":"#"}}}`, `{"self":{"self":{}}}`, true},
		{`{"type":"object","properties":{"self":{"$ref":"#"}}}`, `{"self":5}`, false},

		{`{"$defs":{"node":{"type":"object","properties":{"next":{"$ref":"#/$defs/node"}}}},"$ref":"#/$defs/node"}`, `{"next":{"next":{}}}`, true},
		{`{"$defs":{"node":{"type":"object","properties":{"next":{"$ref":"#/$defs/node"}}}},"$ref":"#/$defs/node"}`, `{"next":5}`, false},

		// pointer-escaped $defs key: escaped $ref resolves to the def's constraints.
		{`{"$defs":{"a/b":{"type":"integer","minimum":0}},"properties":{"n":{"$ref":"#/$defs/a~1b"}}}`, `{"n":5}`, true},
		{`{"$defs":{"a/b":{"type":"integer","minimum":0}},"properties":{"n":{"$ref":"#/$defs/a~1b"}}}`, `{"n":-1}`, false},
		{`{"$defs":{"x~y":{"type":"integer"}},"properties":{"n":{"$ref":"#/$defs/x~0y"}}}`, `{"n":"s"}`, false},
	} {
		s, err := Compile([]byte(tc.schema))
		if err != nil {
			tb.Errorf("compile %q: %v", tc.schema, err)
			continue
		}

		diag, err := validate(s, []byte(tc.data))
		if err != nil {
			tb.Errorf("validate %s against %s: unexpected error: %v", tc.data, tc.schema, err)
			continue
		}
		if (len(diag) == 0) != tc.ok {
			tb.Errorf("validate %s against %s: ok=%v, diag=%v", tc.data, tc.schema, tc.ok, diag)
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

		{`{"if":{"required":["a"]},"then":{"properties":{"b":{"default":2}}}}`, `{"a":1}`, `{"a":1,"b":2}`},
		{`{"if":{"required":["a"]},"then":{"properties":{"b":{"default":2}}}}`, `{"c":1}`, `{"c":1}`}, // condition false: branch not taken
		{`{"if":{"required":["a"]},"else":{"properties":{"b":{"default":2}}}}`, `{"c":1}`, `{"c":1,"b":2}`},
		{`{"if":{"required":["a"]},"else":{"properties":{"b":{"default":2}}}}`, `{"a":1}`, `{"a":1}`},
		{`{"if":{"required":["a"]},"then":{"properties":{"b":{"default":2}}},"else":{"properties":{"c":{"default":3}}}}`, `{"a":1}`, `{"a":1,"b":2}`},
		{`{"if":{"required":["a"]},"then":{"properties":{"b":{"default":2}}},"else":{"properties":{"c":{"default":3}}}}`, `{"z":1}`, `{"z":1,"c":3}`},
		{`{"then":{"properties":{"b":{"default":2}}}}`, `{}`, `{}`}, // no if: branch never runs

		{`{"prefixItems":[{"properties":{"a":{"default":1}}}]}`, `[{},{}]`, `[{"a":1},{}]`},
		{`{"prefixItems":[{},{"properties":{"a":{"default":1}}}]}`, `[{},{}]`, `[{},{"a":1}]`},
		{`{"prefixItems":[{"properties":{"a":{"default":1}}}]}`, `[{},{},{}]`, `[{"a":1},{},{}]`},
		{`{"prefixItems":[{}],"items":{"properties":{"a":{"default":1}}}}`, `[{},{},{}]`, `[{},{"a":1},{"a":1}]`},
		{`{"prefixItems":[{"properties":{"a":{"default":1}}}],"items":{"properties":{"b":{"default":2}}}}`, `[{},{}]`, `[{"a":1},{"b":2}]`},
	} {
		s, err := Compile([]byte(tc.schema))
		if err != nil {
			tb.Errorf("compile %q: %v", tc.schema, err)
			continue
		}

		s.Flags.Set(KeepKeyOrder) // assert input order + appended defaults

		out, _, err := rewrite(s, nil, []byte(tc.in), nil)
		if err != nil {
			tb.Errorf("rewrite %s against %s: %v", tc.in, tc.schema, err)
			continue
		}

		if got := string(out); got != tc.out {
			tb.Errorf("rewrite %s against %s: got %q, want %q", tc.in, tc.schema, got, tc.out)
		}
	}
}

func TestRewriteReorder(tb *testing.T) {
	for _, tc := range []struct {
		schema, in, out string
	}{
		{`{"properties":{"a":{},"b":{}}}`, `{"b":2,"a":1}`, `{"a":1,"b":2}`},             // reorder to declared
		{`{"properties":{"a":{},"b":{}}}`, `{"a":1,"b":2}`, `{"a":1,"b":2}`},             // already canonical
		{`{"properties":{"a":{"default":1},"b":{}}}`, `{"b":2}`, `{"a":1,"b":2}`},        // default into slot
		{`{"properties":{"a":{},"b":{}}}`, `{"c":3,"b":2,"a":1}`, `{"a":1,"b":2,"c":3}`}, // ungoverned last
		{`{"properties":{"a":{}}}`, `{"a":1,"c":3}`, `{"a":1,"c":3}`},                    // governed then ungoverned, unchanged
	} {
		s, err := Compile([]byte(tc.schema))
		if err != nil {
			tb.Errorf("compile %q: %v", tc.schema, err)
			continue
		}

		out, _, err := rewrite(s, nil, []byte(tc.in), nil)
		if err != nil {
			tb.Errorf("rewrite %s against %s: %v", tc.in, tc.schema, err)
			continue
		}

		if got := string(out); got != tc.out {
			tb.Errorf("rewrite %s against %s: got %q, want %q", tc.in, tc.schema, got, tc.out)
		}
	}
}

func TestRewriteFlags(tb *testing.T) {
	for _, tc := range []struct {
		schema, in, out string
		flags           Flags
	}{
		{`{"properties":{"a":{"default":1}}}`, `{}`, `{}`, KeepMissing},                    // default not filled
		{`{"properties":{"a":{},"b":{}}}`, `{"b":2,"a":1}`, `{"b":2,"a":1}`, KeepKeyOrder}, // not reordered
		{`{"properties":{"a":{"default":1},"b":{}}}`, `{"b":2}`, `{"b":2}`, DataPreserve},  // neither
		{`{"properties":{"a":{"default":1},"b":{}}}`, `{"b":2}`, `{"a":1,"b":2}`, 0},       // default-on, reorder-on
	} {
		s, err := Compile([]byte(tc.schema))
		if err != nil {
			tb.Errorf("compile %q: %v", tc.schema, err)
			continue
		}

		s.Flags = tc.flags

		out, _, err := rewrite(s, nil, []byte(tc.in), nil)
		if err != nil {
			tb.Errorf("rewrite %s against %s: %v", tc.in, tc.schema, err)
			continue
		}

		if got := string(out); got != tc.out {
			tb.Errorf("rewrite %s flags=%b against %s: got %q, want %q", tc.in, tc.flags, tc.schema, got, tc.out)
		}
	}
}

func TestWalk(tb *testing.T) {
	delegate := func(c *Applier, s *Schema, op, val Node, h Handler) (Node, error) { return c.Apply(s, op, val, h) }

	// 1. delegating handler reproduces default Validate.
	for _, tc := range []struct {
		schema, data string
		ok           bool
	}{
		{`{"type":"string"}`, `"x"`, true},
		{`{"type":"string"}`, `1`, false},
		{`{"required":["a"]}`, `{}`, false},
	} {
		s, err := Compile([]byte(tc.schema))
		if err != nil {
			tb.Errorf("compile %q: %v", tc.schema, err)
			continue
		}

		wd, we := walk(s, []byte(tc.data), delegate)
		vd, ve := validate(s, []byte(tc.data))

		if we != nil || ve != nil {
			tb.Errorf("walk delegate %s vs validate: unexpected error we=%v ve=%v", tc.data, we, ve)
			continue
		}

		if len(wd) != len(vd) {
			tb.Errorf("walk delegate %s vs validate: wd=%d vd=%d", tc.data, len(wd), len(vd))
		}

		if (len(wd) == 0) != tc.ok {
			tb.Errorf("walk %s against %s: ok=%v diag=%v", tc.data, tc.schema, tc.ok, wd)
		}
	}

	// 2. a custom error aborts the walk and propagates out.
	myErr := errors.New("boom")
	s, _ := Compile([]byte(`{"type":"string"}`))

	fail := func(c *Applier, s *Schema, op, val Node, h Handler) (Node, error) { return Node{}, myErr }
	if _, err := walk(s, []byte(`"x"`), fail); !errors.Is(err, myErr) {
		tb.Errorf("custom error: got %v, want %v", err, myErr)
	}

	// 3. ErrBreak is a clean stop; traversal halts before recursing.
	n := 0
	brk := func(c *Applier, s *Schema, op, val Node, h Handler) (Node, error) {
		n++
		return val, ErrBreak
	}

	bs, _ := Compile([]byte(`{"type":"string","minLength":2}`))
	if d, err := walk(bs, []byte(`"x"`), brk); err != nil || n != 1 || len(d) != 0 {
		tb.Errorf("ErrBreak: err=%v calls=%d diag=%d, want nil/1/0", err, n, len(d))
	}

	// 4. c.Fail records the verdict in a diag; ErrBreak is swallowed, so err is nil.
	rep := func(c *Applier, s *Schema, op, val Node, h Handler) (Node, error) {
		c.Fail(diagHandlerSaysNo, op, val)
		return val, ErrBreak
	}

	if d, err := walk(s, []byte(`"x"`), rep); err != nil || len(d) != 1 || d[0].Code != diagHandlerSaysNo {
		tb.Errorf("Fail: err=%v diag=%+v", err, d)
	}
}

func TestWalkRewrite(tb *testing.T) {
	delegate := func(c *Applier, s *Schema, op, val Node, h Handler) (Node, error) { return c.Apply(s, op, val, h) }

	// delegating handler reproduces the default rewrite: fills defaults, reorders.
	for _, tc := range []struct{ schema, in, out string }{
		{`{"properties":{"a":{"default":1},"b":{}}}`, `{"b":2}`, `{"a":1,"b":2}`},
		{`{"properties":{"a":{},"b":{}}}`, `{"b":2,"a":1}`, `{"a":1,"b":2}`},
	} {
		s, err := Compile([]byte(tc.schema))
		if err != nil {
			tb.Errorf("compile %q: %v", tc.schema, err)
			continue
		}

		out, _, err := rewrite(s, nil, []byte(tc.in), delegate)
		if err != nil {
			tb.Errorf("walkrewrite %s: %v", tc.in, err)
			continue
		}

		if got := string(out); got != tc.out {
			tb.Errorf("walkrewrite %s against %s: got %q, want %q", tc.in, tc.schema, got, tc.out)
		}
	}

	// a custom error aborts WalkRewrite too.
	myErr := errors.New("boom")
	s, _ := Compile([]byte(`{"type":"object"}`))

	fail := func(c *Applier, s *Schema, op, val Node, h Handler) (Node, error) { return Node{}, myErr }
	if _, _, err := rewrite(s, nil, []byte(`{}`), fail); !errors.Is(err, myErr) {
		tb.Errorf("walkrewrite custom error: got %v, want %v", err, myErr)
	}
}

func TestWalkRead(tb *testing.T) {
	// A handler reads every scalar via the public Opcode/Buffer API while still
	// delegating, so validation is unchanged.
	got := map[string]bool{}

	var collect func(b BufferReader, val Node)
	collect = func(b BufferReader, val Node) {
		switch val.Op() {
		case Number, String:
			got[string(b.Span(val))] = true
		case Array:
			for _, e := range b.Nodes(val) {
				collect(b, e)
			}
		case Object:
			ns := b.Nodes(val)
			if len(ns) != 2*int(val.Arg()) { // regression guard: Object Nodes returns 2n words
				tb.Errorf("object nodes: got %d words, want %d (arg=%d)", len(ns), 2*val.Arg(), val.Arg())
			}

			for i := 0; i < len(ns); i += 2 {
				collect(b, ns[i])   // key
				collect(b, ns[i+1]) // value
			}
		}
	}

	h := func(c *Applier, s *Schema, op, val Node, h Handler) (Node, error) {
		collect(c.Buffer.Reader(), val)
		return c.Apply(s, op, val, h)
	}

	s, err := Compile([]byte(`{"type":"object"}`))
	if err != nil {
		tb.Fatal(err)
	}

	if _, err := walk(s, []byte(`{"a":1,"b":["x",2]}`), h); err != nil {
		tb.Fatalf("walk: %v", err)
	}

	want := map[string]bool{"a": true, "1": true, "b": true, "x": true, "2": true}
	for g := range got {
		if !want[g] {
			tb.Errorf("unexpected scalar %q", g)
		}

		delete(want, g)
	}

	if len(want) != 0 {
		tb.Errorf("missing scalars %v, got %v", want, got)
	}
}

func TestWalkSchemaBuf(tb *testing.T) {
	// A handler reads the program arena via SchemaBuf: a Properties op holds 2n
	// words (key, subschema, ...) for its n declared properties.
	var saw bool

	h := func(c *Applier, s *Schema, op, val Node, h Handler) (Node, error) {
		if op.Op() == Properties {
			saw = true

			ns := s.Reader().Nodes(op)
			if len(ns) != 2*int(op.Arg()) {
				tb.Errorf("properties nodes: got %d words, want %d (arg=%d)", len(ns), 2*op.Arg(), op.Arg())
			}

			if got := string(s.Reader().Span(ns[0])); got != "a" {
				tb.Errorf("first property key: got %q, want %q", got, "a")
			}
		}

		return c.Apply(s, op, val, h)
	}

	s, err := Compile([]byte(`{"properties":{"a":{"type":"integer"},"b":{"type":"string"}}}`))
	if err != nil {
		tb.Fatal(err)
	}

	if _, err := walk(s, []byte(`{"a":1,"b":"x"}`), h); err != nil {
		tb.Fatalf("walk: %v", err)
	}

	if !saw {
		tb.Errorf("handler never saw a Properties op")
	}
}

func TestPatternPropsValidate(tb *testing.T) {
	for _, tc := range []struct {
		schema, data string
		ok           bool
	}{
		{`{"patternProperties":{"^a":{"type":"number"}}}`, `{"a1":5}`, true},
		{`{"patternProperties":{"^a":{"type":"number"}}}`, `{"a1":"x"}`, false}, // matched key, wrong type
		{`{"patternProperties":{"^a":{"type":"number"}}}`, `{"b1":"x"}`, true},  // unmatched key, unconstrained

		// a key matching two patterns must satisfy both subschemas.
		{`{"patternProperties":{"^a":{"type":"number"},"1$":{"minimum":3}}}`, `{"a1":5}`, true},
		{`{"patternProperties":{"^a":{"type":"number"},"1$":{"minimum":3}}}`, `{"a1":2}`, false}, // fails second

		{`{"patternProperties":{"^a":{"type":"number"}}}`, `123`, true}, // not applicable to non-objects

		{`{"patternProperties":{"\\t":{"type":"number"}}}`, `{"a\tb":"x"}`, false}, // regex \t matches decoded key
		{`{"patternProperties":{"\\t":{"type":"number"}}}`, `{"axb":"x"}`, true},

		// properties + patternProperties + additionalProperties:false interplay.
		{`{"properties":{"id":{}},"patternProperties":{"^x":{}},"additionalProperties":false}`, `{"id":1,"x1":2}`, true},
		{`{"properties":{"id":{}},"patternProperties":{"^x":{}},"additionalProperties":false}`, `{"y":1}`, false}, // neither named nor matched

		// properties + patternProperties + additionalProperties subschema composing.
		{`{"properties":{"id":{"type":"integer"}},"patternProperties":{"^x":{"type":"string"}},"additionalProperties":{"type":"boolean"}}`, `{"id":1,"x1":"a","ok":true}`, true},
		{`{"properties":{"id":{"type":"integer"}},"patternProperties":{"^x":{"type":"string"}},"additionalProperties":{"type":"boolean"}}`, `{"id":1,"x1":2}`, false},    // pattern-matched wrong type
		{`{"properties":{"id":{"type":"integer"}},"patternProperties":{"^x":{"type":"string"}},"additionalProperties":{"type":"boolean"}}`, `{"id":1,"other":5}`, false}, // additional wrong type
	} {
		s, err := Compile([]byte(tc.schema))
		if err != nil {
			tb.Errorf("compile %q: %v", tc.schema, err)
			continue
		}

		diag, err := validate(s, []byte(tc.data))
		if err != nil {
			tb.Errorf("validate %s against %s: unexpected error: %v", tc.data, tc.schema, err)
			continue
		}
		if (len(diag) == 0) != tc.ok {
			tb.Errorf("validate %s against %s: ok=%v, diag=%v", tc.data, tc.schema, tc.ok, diag)
		}
	}
}

func TestPatternPropsRewrite(tb *testing.T) {
	for _, tc := range []struct{ schema, in, out string }{
		{`{"patternProperties":{"^a":{"properties":{"k":{"default":1}}}}}`, `{"a1":{}}`, `{"a1":{"k":1}}`}, // sub fills nested default
		{`{"patternProperties":{"^a":{"type":"string"}}}`, `{"a1":"x"}`, `{"a1":"x"}`},                     // unchanged: structural sharing
	} {
		s, err := Compile([]byte(tc.schema))
		if err != nil {
			tb.Errorf("compile %q: %v", tc.schema, err)
			continue
		}

		out, _, err := rewrite(s, nil, []byte(tc.in), nil)
		if err != nil {
			tb.Errorf("rewrite %s against %s: %v", tc.in, tc.schema, err)
			continue
		}

		if got := string(out); got != tc.out {
			tb.Errorf("rewrite %s against %s: got %q, want %q", tc.in, tc.schema, got, tc.out)
		}
	}
}

func TestPatternValidate(tb *testing.T) {
	for _, tc := range []struct {
		schema, data string
		ok           bool
	}{
		{`{"type":"string","pattern":"^a.*z$"}`, `"abcz"`, true},
		{`{"type":"string","pattern":"^a.*z$"}`, `"abc"`, false},
		{`{"type":"string","pattern":"^a.*z$"}`, `"zabcz"`, false},

		{`{"pattern":"^a.*z$"}`, `123`, true}, // not applicable to non-strings

		{`{"pattern":"b+"}`, `"abbbc"`, true}, // unanchored substring match
		{`{"pattern":"b+"}`, `"acdef"`, false},

		{`{"pattern":"a.b"}`, `"a\tb"`, true}, // dot matches decoded tab, not raw "\t"
		{`{"pattern":"\\t"}`, `"a\tb"`, true}, // \t regex matches the decoded tab
		{`{"pattern":"\\t"}`, `"axb"`, false},
	} {
		s, err := Compile([]byte(tc.schema))
		if err != nil {
			tb.Errorf("compile %q: %v", tc.schema, err)
			continue
		}

		diag, err := validate(s, []byte(tc.data))
		if err != nil {
			tb.Errorf("validate %s against %s: unexpected error: %v", tc.data, tc.schema, err)
			continue
		}
		if (len(diag) == 0) != tc.ok {
			tb.Errorf("validate %s against %s: ok=%v, diag=%v", tc.data, tc.schema, tc.ok, diag)
		}
	}
}

func TestAdditionalValidate(tb *testing.T) {
	for _, tc := range []struct {
		schema, data string
		ok           bool
	}{
		{`{"additionalProperties":false}`, `{}`, true},
		{`{"additionalProperties":false}`, `{"a":1}`, false},
		{`{"properties":{"a":{}},"additionalProperties":false}`, `{"a":1}`, true}, // named not additional
		{`{"properties":{"a":{}},"additionalProperties":false}`, `{"a":1,"b":2}`, false},

		{`{"additionalProperties":{"type":"string"}}`, `{"x":"y"}`, true}, // no sibling: all additional
		{`{"additionalProperties":{"type":"string"}}`, `{"x":1}`, false},

		{`{"properties":{"a":{"type":"integer"}},"additionalProperties":{"type":"string"}}`, `{"a":1,"b":"y"}`, true},
		{`{"properties":{"a":{"type":"integer"}},"additionalProperties":{"type":"string"}}`, `{"a":1,"b":2}`, false}, // extra wrong type
		{`{"properties":{"a":{"type":"integer"}},"additionalProperties":{"type":"string"}}`, `{"a":"x"}`, false},     // named wrong type
	} {
		s, err := Compile([]byte(tc.schema))
		if err != nil {
			tb.Errorf("compile %q: %v", tc.schema, err)
			continue
		}

		diag, err := validate(s, []byte(tc.data))
		if err != nil {
			tb.Errorf("validate %s against %s: unexpected error: %v", tc.data, tc.schema, err)
			continue
		}
		if (len(diag) == 0) != tc.ok {
			tb.Errorf("validate %s against %s: ok=%v, diag=%v", tc.data, tc.schema, tc.ok, diag)
		}
	}
}

func TestAdditionalRewrite(tb *testing.T) {
	for _, tc := range []struct{ schema, in, out string }{
		{`{"additionalProperties":{"properties":{"k":{"default":1}}}}`, `{"x":{}}`, `{"x":{"k":1}}`},                                       // sub rewrites additional value
		{`{"additionalProperties":{"type":"string"}}`, `{"x":"y"}`, `{"x":"y"}`},                                                           // unchanged: structural sharing
		{`{"properties":{"a":{"default":1},"b":{}},"additionalProperties":{"type":"string"}}`, `{"c":"z","b":2}`, `{"a":1,"b":2,"c":"z"}`}, // props reorder+default, then additional
	} {
		s, err := Compile([]byte(tc.schema))
		if err != nil {
			tb.Errorf("compile %q: %v", tc.schema, err)
			continue
		}

		out, _, err := rewrite(s, nil, []byte(tc.in), nil)
		if err != nil {
			tb.Errorf("rewrite %s against %s: %v", tc.in, tc.schema, err)
			continue
		}

		if got := string(out); got != tc.out {
			tb.Errorf("rewrite %s against %s: got %q, want %q", tc.in, tc.schema, got, tc.out)
		}
	}
}

func TestXHook(tb *testing.T) {
	var rewriting bool

	// The x-type:upper keyword is now an inert Ext node; a Walk handler detects it
	// and uppercases the governed string value, replacing the old registered hook.
	upper := func(c *Applier, s *Schema, op, val Node, h Handler) (Node, error) {
		if op.Op() != Ext {
			return c.Apply(s, op, val, h)
		}

		rewriting = c.Rewriting()

		kids := s.Reader().Nodes(op)
		key := string(s.Reader().String(kids[0]))
		value := string(s.Reader().String(kids[1]))

		if key == "x-type" && value == "upper" && c.Rewriting() && val.Op() == String {
			return c.Buffer.Writer().Span(String, bytes.ToUpper(c.Buffer.Reader().Span(val))), nil
		}

		return val, nil
	}

	// 1. end-to-end rewrite via the Walk handler (no SetXHook).
	var s Schema

	if err := s.Compile([]byte(`{"properties":{"name":{"x-type":"upper"}}}`)); err != nil {
		tb.Fatalf("compile: %v", err)
	}

	out, _, err := rewrite(&s, nil, []byte(`{"name":"hi"}`), upper)
	if err != nil {
		tb.Fatalf("rewrite: %v", err)
	}

	if got := string(out); got != `{"name":"HI"}` {
		tb.Errorf("rewrite: got %q, want %q", got, `{"name":"HI"}`)
	}

	// 2. the x- keyword survives compile -> format (Ext round-trips).
	if got := string(s.Format(nil)); got != `{"properties":{"name":{"x-type":"upper"}}}` {
		tb.Errorf("format: got %q", got)
	}

	// 3. read-only: Walk is clean and the handler observes Rewriting()==false, so
	// the value is not uppercased.
	rewriting = true

	if d, err := walk(&s, []byte(`{"name":"hi"}`), upper); err != nil || len(d) != 0 {
		tb.Errorf("walk: err=%v diag=%v", err, d)
	}

	if rewriting {
		tb.Errorf("walk: handler saw Rewriting()==true")
	}
}

func TestXHookUnregistered(tb *testing.T) {
	// An x- keyword with no hook stays Raw: compiles and round-trips, no dispatch.
	var s Schema

	if err := s.Compile([]byte(`{"x-foo":{"a":[1,2]},"type":"object"}`)); err != nil {
		tb.Fatalf("compile: %v", err)
	}

	if got := string(s.Format(nil)); got != `{"type":"object","x-foo":{"a":[1,2]}}` {
		tb.Errorf("format: got %q", got)
	}

	if d, err := validate(&s, []byte(`{}`)); err != nil || len(d) != 0 {
		tb.Errorf("validate: err=%v diag=%v", err, d)
	}
}

func TestXTypeIDToObject(tb *testing.T) {
	// An x-type:id keyword rewrites the governed "entity/version" string into an
	// {"entity": <string>, "version": <int>} object; version is omitted when it is
	// 0 or absent. A sibling type:string check runs first (on the still-string
	// value), so it passes before the Ext swaps in the object.
	idToObject := func(c *Applier, s *Schema, op, val Node, h Handler) (Node, error) {
		if op.Op() != Ext {
			return c.Apply(s, op, val, h)
		}

		kids := s.Reader().Nodes(op)
		key := string(s.Reader().String(kids[0]))
		value := string(s.Reader().String(kids[1]))

		if key != "x-type" || value != "id" || !c.Rewriting() || val.Op() != String {
			return val, nil
		}

		// String() bytes are transient; string(...) copies them out.
		entity, ver, _ := strings.Cut(string(c.Buffer.Reader().String(val)), "/")

		w := c.Buffer.Writer()
		kv := []Node{w.Bytes([]byte("entity")), w.Bytes([]byte(entity))}

		if n, _ := strconv.Atoi(ver); n != 0 {
			kv = append(kv, w.Bytes([]byte("version")), MakeInt(int64(n)))
		}

		return w.Object(kv...), nil
	}

	cases := []struct{ schema, in, out string }{
		{`{"properties":{"user":{"x-type":"id"}}}`, `{"user":"u1/3"}`, `{"user":{"entity":"u1","version":3}}`},
		{`{"properties":{"user":{"type":"string","x-type":"id"}}}`, `{"user":"u1/0"}`, `{"user":{"entity":"u1"}}`},
		{`{"properties":{"user":{"x-type":"id"}}}`, `{"user":"u1"}`, `{"user":{"entity":"u1"}}`},
		{`{"items":{"x-type":"id"}}`, `["a/1","b/0","c"]`, `[{"entity":"a","version":1},{"entity":"b"},{"entity":"c"}]`},
	}

	for i, tc := range cases {
		var s Schema

		if err := s.Compile([]byte(tc.schema)); err != nil {
			tb.Fatalf("[%d] compile: %v", i, err)
		}

		out, diag, err := rewrite(&s, nil, []byte(tc.in), idToObject)
		if err != nil {
			tb.Fatalf("[%d] rewrite: %v", i, err)
		}

		if string(out) != tc.out {
			tb.Errorf("[%d] got %q want %q (diag %v)", i, out, tc.out, diag)
		}
	}
}

func TestWalkFromJSON(tb *testing.T) {
	// A handler mints a whole structured value from JSON text via FromJSON.
	s, err := Compile([]byte(`{}`))
	if err != nil {
		tb.Fatal(err)
	}

	h := func(c *Applier, s *Schema, op, val Node, h Handler) (Node, error) {
		if val.Op() == Number {
			return c.Buffer.Writer().FromJSON([]byte(`{"wrapped":5}`))
		}

		return c.Apply(s, op, val, h)
	}

	out, _, err := rewrite(s, nil, []byte(`5`), h)
	if err != nil {
		tb.Fatalf("walkrewrite: %v", err)
	}

	if got := string(out); got != `{"wrapped":5}` {
		tb.Errorf("walkrewrite: got %q, want %q", got, `{"wrapped":5}`)
	}
}

func TestWalkEmitArray(tb *testing.T) {
	// Array-element rewrite propagates: the engine descends via items and rebuilds
	// the array from the subschema's returned values.
	s, err := Compile([]byte(`{"items":{}}`))
	if err != nil {
		tb.Fatal(err)
	}

	repl := func(c *Applier, s *Schema, op, val Node, h Handler) (Node, error) {
		if val.Op() == Number {
			return c.Buffer.Writer().Span(Number, []byte("42")), nil
		}

		return c.Apply(s, op, val, h)
	}

	out, _, err := rewrite(s, nil, []byte(`[1,2,3]`), repl)
	if err != nil {
		tb.Fatalf("walkrewrite: %v", err)
	}

	if got := string(out); got != `[42,42,42]` {
		tb.Errorf("walkrewrite: got %q, want %q", got, `[42,42,42]`)
	}

	// structural sharing: a pure delegate leaves the input byte-identical.
	pass := func(c *Applier, s *Schema, op, val Node, h Handler) (Node, error) { return c.Apply(s, op, val, h) }

	out, _, err = rewrite(s, nil, []byte(`[1,2,3]`), pass)
	if err != nil {
		tb.Fatalf("walkrewrite delegate: %v", err)
	}

	if got := string(out); got != `[1,2,3]` {
		tb.Errorf("walkrewrite delegate: got %q, want %q", got, `[1,2,3]`)
	}
}

func TestWalkEmit(tb *testing.T) {
	// A handler produces values via Emit, replacing matched scalars through
	// WalkRewrite; non-matching values fall through to the default.
	for _, tc := range []struct {
		schema, in, out string
		emit            Opcode
		bytes           string
	}{
		{`{}`, `5`, `42`, Number, `42`},
		{`{}`, `"a"`, `"hi"`, String, `hi`},
		{`{"properties":{"a":{}}}`, `{"a":5,"b":7}`, `{"a":42,"b":7}`, Number, `42`}, // only governed scalar replaced
	} {
		s, err := Compile([]byte(tc.schema))
		if err != nil {
			tb.Errorf("compile %q: %v", tc.schema, err)
			continue
		}

		h := func(c *Applier, s *Schema, op, val Node, h Handler) (Node, error) {
			if val.Op() == tc.emit {
				return c.Buffer.Writer().Span(tc.emit, []byte(tc.bytes)), nil
			}

			return c.Apply(s, op, val, h)
		}

		out, _, err := rewrite(s, nil, []byte(tc.in), h)
		if err != nil {
			tb.Errorf("walkrewrite %s: %v", tc.in, err)
			continue
		}

		if got := string(out); got != tc.out {
			tb.Errorf("walkrewrite %s: got %q, want %q", tc.in, got, tc.out)
		}
	}
}

func TestRewriteCanonical(tb *testing.T) {
	s, err := Compile([]byte(`{"type":"object"}`))
	if err != nil {
		tb.Fatal(err)
	}

	out, diag, err := rewrite(s, nil, []byte(`{ "a" : 1 , "b" : [ 2, 3 ] }`), nil)
	if err != nil || len(diag) != 0 {
		tb.Fatalf("err=%v diag=%v", err, diag)
	}

	if got := string(out); got != `{"a":1,"b":[2,3]}` {
		tb.Errorf("rewrite: %q", got)
	}
}

func TestWalkHandlerSwap(tb *testing.T) {
	// The handler passed to Apply is the one that sees the subtree's children.
	// Passing self keeps the handler in the loop; passing nil runs the subtree
	// with default behaviour only. Either way the built-in validation still runs.
	s, err := Compile([]byte(`{"properties":{"a":{"type":"string"}}}`))
	if err != nil {
		tb.Fatal(err)
	}

	var seen int
	self := func(c *Applier, s *Schema, op, val Node, h Handler) (Node, error) {
		seen++
		return c.Apply(s, op, val, h)
	}

	var top int
	cut := func(c *Applier, s *Schema, op, val Node, h Handler) (Node, error) {
		top++
		return c.Apply(s, op, val, nil) // descendants fall to default; handler not re-entered
	}

	ds, es := walk(s, []byte(`{"a":1}`), self)
	dc, ec := walk(s, []byte(`{"a":1}`), cut)

	if es != nil || ec != nil {
		tb.Fatalf("errors: self=%v cut=%v", es, ec)
	}

	// Both still catch the type mismatch — validation is unaffected by the swap.
	if len(ds) != 1 || len(dc) != 1 {
		tb.Fatalf("diags: self=%d cut=%d, want 1/1", len(ds), len(dc))
	}

	if top != 1 {
		tb.Errorf("cut handler calls=%d, want 1 (nil delegate stops re-entry)", top)
	}

	if seen <= top {
		tb.Errorf("self handler calls=%d, want > cut's %d", seen, top)
	}
}

func TestWalkFilterDiags(tb *testing.T) {
	// A handler validates a subtree the normal way, then drops the diagnostics it
	// produced from the tail — the snapshot/filter pattern Diags enables.
	s, err := Compile([]byte(`{"properties":{"a":{"type":"string"},"b":{"type":"string"}}}`))
	if err != nil {
		tb.Fatal(err)
	}

	// Suppress diagnostics raised anywhere under property "b".
	h := func(c *Applier, s *Schema, op, val Node, h Handler) (Node, error) {
		mark := len(c.Diags)

		nv, err := c.Apply(s, op, val, h)
		if err != nil {
			return nv, err
		}

		if string(c.Buffer.Reader().AppendPointer(nil, c.Steps)) == ".b" {
			c.Diags = c.Diags[:mark]
		}

		return nv, nil
	}

	d, err := walk(s, []byte(`{"a":1,"b":2}`), h)
	if err != nil {
		tb.Fatal(err)
	}

	if len(d) != 1 {
		tb.Fatalf("diags=%d, want 1 (b suppressed, a kept)", len(d))
	}

	if d[0].Code != TypeMismatch {
		tb.Errorf("code=%v", d[0].Code)
	}
}

// TestSynthesized walks schemas against values a handler produced instead of
// decoded ones. Such values carry no source span, so the keyword checks must
// treat them as first-class rather than panic reaching for their bytes.
func TestSynthesized(tb *testing.T) {
	obj := func(w BufferWriter) Node { return w.Object(w.String("k"), w.String("v")) }

	for _, tc := range []struct {
		schema string
		make   func(BufferWriter) Node
		ok     bool
	}{
		{`{"type":"integer"}`, func(w BufferWriter) Node { return w.Int(5) }, true},
		{`{"type":"integer"}`, func(w BufferWriter) Node { return w.Float(2) }, true},
		{`{"type":"integer"}`, func(w BufferWriter) Node { return w.Float(1.5) }, false},
		{`{"type":"number"}`, func(w BufferWriter) Node { return w.Float(1.5) }, true},
		{`{"type":"string"}`, func(w BufferWriter) Node { return w.Int(5) }, false},
		{`{"type":"integer"}`, func(w BufferWriter) Node { return w.String("x") }, false},
		{`{"minLength":2}`, func(w BufferWriter) Node { return w.String("xy") }, true},
		{`{"minLength":2}`, func(w BufferWriter) Node { return w.String("x") }, false},
		{`{"pattern":"^a+$"}`, func(w BufferWriter) Node { return w.String("aaa") }, true},
		{`{"pattern":"^a+$"}`, func(w BufferWriter) Node { return w.String("b") }, false},
		{`{"type":"object"}`, obj, true},
		{`{"type":"string"}`, obj, false},
		{`{"minimum":10}`, func(w BufferWriter) Node { return w.Int(10) }, true},
		{`{"minimum":10}`, func(w BufferWriter) Node { return w.Int(9) }, false},
		{`{"exclusiveMaximum":10}`, func(w BufferWriter) Node { return w.Float(9.5) }, true},
		{`{"exclusiveMaximum":10}`, func(w BufferWriter) Node { return w.Float(10) }, false},
		{`{"multipleOf":3}`, func(w BufferWriter) Node { return w.Int(9) }, true},
		{`{"multipleOf":3}`, func(w BufferWriter) Node { return w.Int(10) }, false},
		{`{"multipleOf":0.5}`, func(w BufferWriter) Node { return w.Int(7) }, true},
		{`{"multipleOf":0.145}`, func(w BufferWriter) Node { return w.Span(Number, []byte("4.35")) }, true},
		{`{"multipleOf":0.145}`, func(w BufferWriter) Node { return w.Span(Number, []byte("4.4")) }, false},
		{`{"multipleOf":2}`, func(w BufferWriter) Node { return w.Int(-4) }, true},
		{`{"multipleOf":2}`, func(w BufferWriter) Node { return w.Int(-5) }, false},
		{`{"const":5}`, func(w BufferWriter) Node { return w.Int(5) }, true},
		{`{"const":5}`, func(w BufferWriter) Node { return w.Int(6) }, false},
		{`{"enum":[1,2.5,"x"]}`, func(w BufferWriter) Node { return w.Float(2.5) }, true},
		{`{"enum":[1,2.5,"x"]}`, func(w BufferWriter) Node { return w.Int(3) }, false},
		{`{"const":{"k":"v"}}`, obj, true},
	} {
		s, err := Compile([]byte(tc.schema))
		if err != nil {
			tb.Errorf("compile %s: %v", tc.schema, err)
			continue
		}

		first := true
		h := func(c *Applier, s *Schema, op, val Node, h Handler) (Node, error) {
			if first {
				first, val = false, tc.make(c.Buffer.Writer())
			}

			return c.Apply(s, op, val, h)
		}

		d, err := walk(s, []byte(`null`), h)
		if err != nil {
			tb.Errorf("walk %s: %v", tc.schema, err)
			continue
		}

		if (len(d) == 0) != tc.ok {
			tb.Errorf("walk synthesized against %s: ok=%v diag=%v", tc.schema, tc.ok, d)
		}

		// A synthesized value never was text, so it has no place in the document —
		// the keyword it failed still has its place in the schema.
		if len(d) != 0 {
			if off, end, ok := d[0].Val.Src(); ok {
				tb.Errorf("walk synthesized against %s: value span=%d:%d, want none", tc.schema, off, end)
			}

			if _, _, ok := d[0].Op.Src(); !ok {
				tb.Errorf("walk synthesized against %s: the keyword has no place in the schema", tc.schema)
			}
		}
	}
}

func TestEscapedStrings(tb *testing.T) {
	for _, tc := range []struct {
		schema, data string
		ok           bool
	}{
		{`{"properties":{"caf\u00e9":{"pattern":"^a+$"}}}`, `{"café":"aaa"}`, true},
		{`{"properties":{"caf\u00e9":{"pattern":"^a+$"}}}`, `{"café":"bbb"}`, false},
		{`{"properties":{"café":{"pattern":"^\u0061+$"}}}`, `{"caf\u00e9":"aaa"}`, true},
		{`{"properties":{"café":{"pattern":"^\u0061+$"}}}`, `{"caf\u00e9":"bbb"}`, false},
		{`{"required":["caf\u00e9"]}`, `{"café":1}`, true},
		{`{"required":["café"]}`, `{"tea":1}`, false},
		{`{"$defs":{"caf\u00e9":{"type":"integer"}},"$ref":"#/$defs/café"}`, `5`, true},
		{`{"$defs":{"café":{"type":"integer"}},"$ref":"#/$defs/caf\u00e9"}`, `"x"`, false},
		{`{"enum":["caf\u00e9"]}`, `"café"`, true},
		{`{"enum":["café"]}`, `"caf\u00e9"`, true},
		{`{"enum":["café"]}`, `"tea"`, false},
		{`{"const":"caf\u00e9"}`, `"café"`, true},
	} {
		s, err := Compile([]byte(tc.schema))
		if err != nil {
			tb.Errorf("compile %q: %v", tc.schema, err)
			continue
		}

		d, err := validate(s, []byte(tc.data))
		if err != nil {
			tb.Errorf("validate %q against %q: %v", tc.data, tc.schema, err)
			continue
		}

		if (len(d) == 0) != tc.ok {
			tb.Errorf("validate %q against %q: ok=%v, diag=%v", tc.data, tc.schema, tc.ok, d)
		}
	}
}

func TestStrLenRunes(tb *testing.T) {
	for _, tc := range []struct {
		schema, data string
		ok           bool
	}{
		{`{"minLength":4,"maxLength":4}`, `"café"`, true},
		{`{"minLength":4,"maxLength":4}`, `"caf\u00e9"`, true},
		{`{"maxLength":3}`, `"café"`, false},
		{`{"maxLength":3}`, `"caf\u00e9"`, false},
		{`{"minLength":5}`, `"café"`, false},
	} {
		s, err := Compile([]byte(tc.schema))
		if err != nil {
			tb.Errorf("compile %q: %v", tc.schema, err)
			continue
		}

		d, err := validate(s, []byte(tc.data))
		if err != nil {
			tb.Errorf("validate %q against %q: %v", tc.data, tc.schema, err)
			continue
		}

		if (len(d) == 0) != tc.ok {
			tb.Errorf("validate %q against %q: ok=%v, diag=%v", tc.data, tc.schema, tc.ok, d)
		}
	}
}

func TestStepsRef(tb *testing.T) {
	s, err := Compile([]byte(`{"$defs":{"u":{"properties":{"age":{"type":"integer"}}}},"properties":{"users":{"items":{"$ref":"#/$defs/u"}}}}`))
	if err != nil {
		tb.Fatal(err)
	}

	seen := false
	h := func(c *Applier, s *Schema, op, val Node, h Handler) (Node, error) {
		if op.Op() == Type {
			seen = true

			if len(c.Steps) != 4 || c.Depth != 3 {
				tb.Fatalf("steps=%d depth=%d, want 4 3: %v", len(c.Steps), c.Depth, c.Steps)
			}

			st := c.Steps[2]
			if st.Op.Op() != Ref || st.Value.Op() != Ref || st.DataKey.Op() != None {
				tb.Errorf("ref step: Op=%v Value=%v DataKey=%v, want Ref Ref None", st.Op.Op(), st.Value.Op(), st.DataKey)
			}
			if st.Doc != s {
				tb.Errorf("ref step: internal ref changed document")
			}
			if got := string(c.Buffer.Reader().AppendPointer(nil, c.Steps)); got != ".users[0].age" {
				tb.Errorf("ref pointer %q, want %q", got, ".users[0].age")
			}
		}

		return c.Apply(s, op, val, h)
	}

	if _, err := walk(s, []byte(`{"users":[{"age":"x"}]}`), h); err != nil {
		tb.Fatal(err)
	}
	if !seen {
		tb.Errorf("Type node behind the ref never applied")
	}
}

func TestStepsExternalRef(tb *testing.T) {
	common, err := Compile([]byte(`{"$defs":{"Id":{"type":"string"}}}`))
	if err != nil {
		tb.Fatal(err)
	}

	var root Schema
	root.AddDoc("urn:objects:common", common)

	if err := root.Compile([]byte(`{"properties":{"id":{"$ref":"urn:objects:common#/$defs/Id"}}}`)); err != nil {
		tb.Fatal(err)
	}

	seen := false
	h := func(c *Applier, s *Schema, op, val Node, h Handler) (Node, error) {
		// the handler is told which document the node lives in: the referrer
		// before the $ref, the registered one after it
		if op.Op() == Properties && s != &root {
			tb.Errorf("handler doc at properties: got %p, want the referrer %p", s, &root)
		}

		if op.Op() == Ref && s != &root {
			tb.Errorf("handler doc at the ref: got %p, want the referrer %p", s, &root)
		}

		if op.Op() == Type {
			seen = true

			if s != common {
				tb.Errorf("handler doc behind the ref: got %p, want %p", s, common)
			}

			ref := c.Steps[len(c.Steps)-1]
			if ref.Op.Op() != Ref || ref.DataKey.Op() != None {
				tb.Errorf("ref step: Op=%v DataKey=%v, want Ref None", ref.Op.Op(), ref.DataKey)
			}
			if ref.Doc != common {
				tb.Errorf("ref step: Doc is not the registered document")
			}
			if got := TypesOf(s.Reader().Keyword(ref.Sub, Type)); got != TypeString {
				tb.Errorf("target type %v, want %v", got, TypeString)
			}
		}

		return c.Apply(s, op, val, h)
	}

	if _, err := walk(&root, []byte(`{"id":5}`), h); err != nil {
		tb.Fatal(err)
	}
	if !seen {
		tb.Errorf("Type node behind the external ref never applied")
	}
}

func TestStepsDepth(tb *testing.T) {
	for _, schema := range []string{
		`{"properties":{"a":{"properties":{"b":{"type":"integer"}}}}}`,
		`{"allOf":[{"properties":{"a":{"allOf":[{"properties":{"b":{"type":"integer"}}}]}}}]}`,
		`{"$defs":{"t":{"properties":{"b":{"type":"integer"}}}},"properties":{"a":{"$ref":"#/$defs/t"}}}`,
		`{"properties":{"a":{"if":{"type":"object"},"then":{"properties":{"b":{"type":"integer"}}}}}}`,
	} {
		s, err := Compile([]byte(schema))
		if err != nil {
			tb.Errorf("compile %q: %v", schema, err)
			continue
		}

		seen := false
		h := func(c *Applier, s *Schema, op, val Node, h Handler) (Node, error) {
			if op.Op() == Type && val.Op() == String {
				seen = true

				if c.Depth != 2 {
					tb.Errorf("%q: Depth=%d, want 2", schema, c.Depth)
				}
				if got := string(c.Buffer.Reader().AppendPointer(nil, c.Steps)); got != ".a.b" {
					tb.Errorf("%q: pointer %q, want %q", schema, got, ".a.b")
				}
			}

			return c.Apply(s, op, val, h)
		}

		if _, err := walk(s, []byte(`{"a":{"b":"x"}}`), h); err != nil {
			tb.Errorf("walk %q: %v", schema, err)
			continue
		}
		if !seen {
			tb.Errorf("%q: nested Type never applied", schema)
		}
	}
}

func TestDiagCollector(tb *testing.T) {
	s, err := Compile([]byte(`{"properties":{"users":{"items":{"$ref":"#/$defs/user"}},"tag":{"not":{"type":"integer"}},"kind":{"oneOf":[{"type":"integer"},{"type":"boolean"}]}},"$defs":{"user":{"properties":{"age":{"type":"integer"}}}}}`))
	if err != nil {
		tb.Fatal(err)
	}

	type rec struct {
		diag Diag
		path string
	}

	var recs []rec

	// A finding an outer frame re-sees is already recorded, at the frame that
	// raised it; anything else is new and claims that slot.
	h := func(c *Applier, s *Schema, op, val Node, h Handler) (Node, error) {
		n := len(c.Diags)

		v, err := c.Apply(s, op, val, h)
		if err != nil {
			return v, err
		}

		for i := n; i < len(c.Diags); i++ {
			if i < len(recs) && sameDiag(recs[i].diag, c.Diags[i]) {
				continue
			}

			r := rec{diag: c.Diags[i], path: string(c.Buffer.Reader().AppendPointer(nil, c.Steps))}

			if i < len(recs) {
				recs[i] = r
				continue
			}

			recs = append(recs, r)
		}

		return v, nil
	}

	diags, err := walk(s, []byte(`{"users":[{"age":"x"}],"tag":"ok","kind":"neither"}`), h)
	if err != nil {
		tb.Fatal(err)
	}

	if len(diags) != 2 {
		tb.Fatalf("diags=%d, want 2: %+v", len(diags), diags)
	}
	if len(recs) < len(diags) {
		tb.Fatalf("records=%d, want at least %d", len(recs), len(diags))
	}

	recs = recs[:len(diags)]

	for i, want := range []string{".users[0].age", ".kind"} {
		if !sameDiag(recs[i].diag, diags[i]) {
			tb.Errorf("record %d: %+v, want %+v", i, recs[i].diag, diags[i])
		}
		if recs[i].path != want {
			tb.Errorf("record %d: path %q, want %q", i, recs[i].path, want)
		}
	}
}

func TestHandlerKeyword(tb *testing.T) {
	s, err := Compile([]byte(`{"properties":{"a":{"type":["integer","null"]},"b":{"minLength":3,"maxLength":5}}}`))
	if err != nil {
		tb.Fatal(err)
	}

	var types Types
	var minlen, maxlen int64

	h := func(c *Applier, s *Schema, op, val Node, h Handler) (Node, error) {
		switch op.Op() {
		case Type:
			types = TypesOf(op)
		case MinLen:
			minlen = op.Imm()
		case MaxLen:
			maxlen = op.Imm()
		}

		return c.Apply(s, op, val, h)
	}

	d, err := walk(s, []byte(`{"a":"x","b":"y"}`), h)
	if err != nil {
		tb.Fatal(err)
	}

	if len(d) != 2 {
		tb.Fatalf("diags=%d, want 2: %+v", len(d), d)
	}
	if types != TypeInteger|TypeNull {
		tb.Errorf("types=%v, want %v", types, TypeInteger|TypeNull)
	}
	if minlen != 3 || maxlen != 5 {
		tb.Errorf("bounds=%d/%d, want 3/5", minlen, maxlen)
	}
}

func sameDiag(a, b Diag) bool {
	return a.Code == b.Code && a.Op == b.Op && a.Val == b.Val
}

func TestSaveSteps(tb *testing.T) {
	src := `{"$defs":{"u":{"properties":{"age":{"type":"integer"}}}},"properties":{"a":{"items":{"minLength":3}},"u":{"$ref":"#/$defs/u"}}}`
	data := []byte(`{"a":["abc","x"],"u":{"age":"y"}}`)

	s, err := Compile([]byte(src))
	if err != nil {
		tb.Fatal(err)
	}

	d, err := validate(s, data)
	if err != nil || len(d) != 2 {
		tb.Fatalf("diags=%v err=%v, want 2", d, err)
	}

	for i, x := range d {
		if x.Steps != nil {
			tb.Errorf("diag %d: Steps=%v, want nil without SaveSteps", i, x.Steps)
		}
	}

	s.Flags.Set(SaveSteps)

	// DataKey nodes live in the walk's data buffer, so the path is rendered
	// through the Applier that walked, not the schema program
	var a Applier

	d, err = a.Validate(s, data)
	if err != nil || len(d) != 2 {
		tb.Fatalf("diags=%v err=%v, want 2", d, err)
	}

	r := a.Buffer.Reader()

	for i, tc := range []struct {
		code  DiagCode
		path  string
		steps int
	}{
		{TooShort, ".a[1]", 2},
		{TypeMismatch, ".u.age", 3},
	} {
		if d[i].Code != tc.code || len(d[i].Steps) != tc.steps {
			tb.Errorf("diag %d: %v with %d steps, want %v with %d", i, d[i].Code, len(d[i].Steps), tc.code, tc.steps)
		}

		if got := string(r.AppendPointer(nil, d[i].Steps)); got != tc.path {
			tb.Errorf("diag %d: path %q, want %q", i, got, tc.path)
		}
	}

	if ref := d[1].Steps[1]; ref.Op.Op() != Ref || ref.DataKey.Op() != None {
		tb.Errorf("ref step: Op=%v DataKey=%v, want Ref None", ref.Op.Op(), ref.DataKey)
	}

	if &d[0].Steps[0] == &d[1].Steps[0] {
		tb.Errorf("diags share a Steps backing array")
	}
}

// validate, walk and rewrite run a schema on a fresh Applier — the common case
// where the workspace is not read afterwards.
func validate(s *Schema, doc []byte) ([]Diag, error) {
	var a Applier

	return a.Validate(s, doc)
}

func walk(s *Schema, doc []byte, h Handler) ([]Diag, error) {
	var a Applier

	return a.Walk(s, Node{}, doc, h)
}

func rewrite(s *Schema, buf, doc []byte, h Handler) ([]byte, []Diag, error) {
	var a Applier

	return a.Rewrite(s, Node{}, doc, buf, h)
}

// found renders each diag as "message@<located source>" so a test can assert the
// set of findings (message + where it points) order-independently.
func found(doc []byte, d []Diag) []string {
	out := make([]string, 0, len(d))
	for _, x := range d {
		off, end := x.valSpan()
		out = append(out, x.Code.String()+"@"+string(doc[off:end]))
	}

	sort.Strings(out)

	return out
}

func wantSet(tb *testing.T, name string, doc []byte, d []Diag, want ...string) {
	tb.Helper()

	got := found(doc, d)
	sort.Strings(want)

	if strings.Join(got, " | ") != strings.Join(want, " | ") {
		tb.Errorf("%s: diags = %v, want %v", name, got, want)
	}
}

func mustCompile(tb *testing.T, src string) *Schema {
	tb.Helper()

	var s Schema
	if err := s.Compile([]byte(src)); err != nil {
		tb.Fatalf("compile %q: %v", src, err)
	}

	return &s
}

// TestWalkFragment walks a subschema reached with Lookup: the document is the
// fragment that node describes, and the root's own keywords never apply.
func TestWalkFragment(tb *testing.T) {
	sc := mustCompile(tb, `{
		"properties": {
			"title": {"type":"string"},
			"spec": {"oneOf": [
				{"properties": {"kind": {"const":"a"}, "n": {"type":"integer"}}, "required": ["kind"]},
				{"properties": {"kind": {"const":"b"}, "s": {"type":"string"}}, "required": ["kind"]}
			]}
		},
		"required": ["title"]
	}`)

	_, spec, err := sc.Lookup("#/properties/spec")
	if err != nil {
		tb.Fatalf("lookup: %v", err)
	}

	var a Applier

	for _, tc := range []struct {
		name, doc string
		want      []string
	}{
		{"a", `{"kind":"a","n":1}`, nil},
		{"b", `{"kind":"b","s":"x"}`, nil},
		{"neither", `{"kind":"a","n":"x"}`, []string{"matches none of the schemas@" + `{"kind":"a","n":"x"}`}},
	} {
		d, err := a.Walk(sc, spec, []byte(tc.doc), nil)
		if err != nil {
			tb.Errorf("%s: unexpected error: %v", tc.name, err)
			continue
		}

		wantSet(tb, tc.name, []byte(tc.doc), d, tc.want...)
	}

	// the same document from the root: the fragment's keywords no longer govern
	// it, and the root's required does
	d, err := a.Walk(sc, Node{}, []byte(`{"kind":"a","n":"x"}`), nil)
	if err != nil {
		tb.Fatalf("root: %v", err)
	}

	wantSet(tb, "root", []byte(`{"kind":"a","n":"x"}`), d, "missing required property@"+`{"kind":"a","n":"x"}`)

	if _, op, err := sc.Lookup("#"); err != nil || op != sc.Root() {
		tb.Fatalf("lookup root: op=%v err=%v", op, err)
	}
}

// TestApplierReuse runs one Applier over two schemas back to back: the
// workspace is reset, never carried over.
func TestApplierReuse(tb *testing.T) {
	one := mustCompile(tb, `{"type":"object","properties":{"a":{"type":"integer"}},"required":["a"]}`)
	two := mustCompile(tb, `{"type":"array","items":{"minLength":3}}`)

	var a Applier

	one.Flags.Set(SaveSteps)
	two.Flags.Set(SaveSteps)

	d, err := a.Validate(one, []byte(`{"a":"x"}`))
	if err != nil || len(d) != 1 || d[0].Code != TypeMismatch {
		tb.Fatalf("first: diags=%v err=%v", d, err)
	}

	if got := string(a.Buffer.Reader().AppendPointer(nil, d[0].Steps)); got != ".a" {
		tb.Errorf("first: path %q, want %q", got, ".a")
	}

	d, err = a.Validate(two, []byte(`["abc","y"]`))
	if err != nil || len(d) != 1 || d[0].Code != TooShort {
		tb.Fatalf("second: diags=%v err=%v", d, err)
	}

	if got := string(a.Buffer.Reader().AppendPointer(nil, d[0].Steps)); got != "[1]" {
		tb.Errorf("second: path %q, want %q", got, "[1]")
	}

	if a.Depth != 0 || len(a.Steps) != 0 {
		tb.Errorf("after the walk: Depth=%d Steps=%d, want 0 0", a.Depth, len(a.Steps))
	}

	if d, err := a.Validate(two, []byte(`["abc"]`)); err != nil || len(d) != 0 {
		tb.Errorf("third: diags=%v err=%v, want none", d, err)
	}
}
