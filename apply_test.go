package schema

import (
	"bytes"
	"errors"
	"strconv"
	"strings"
	"testing"
)

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

		{
			`{"type":"object","properties":{"items":{"type":"array","items":{"type":"object","required":["id"]}}}}`,
			`{"items":[{"id":1},{"id":2}]}`, true,
		},
		{
			`{"type":"object","properties":{"items":{"type":"array","items":{"type":"object","required":["id"]}}}}`,
			`{"items":[{"id":1},{"name":"x"}]}`, false,
		},

		{`{"title":"x","type":"string"}`, `"y"`, true}, // annotation ignored

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

		diag, err := s.Validate([]byte(tc.data))
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
	} {
		s, err := Compile([]byte(tc.schema))
		if err != nil {
			tb.Errorf("compile %q: %v", tc.schema, err)
			continue
		}

		s.Flags.Set(KeepKeyOrder) // assert input order + appended defaults

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

		out, _, err := s.Rewrite(nil, []byte(tc.in))
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
	delegate := func(c *Applier, op, val Opcode, h Handler) (Opcode, error) { return c.Apply(op, val, h) }

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

		wd, we := s.Walk([]byte(tc.data), delegate)
		vd, ve := s.Validate([]byte(tc.data))

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

	fail := func(c *Applier, op, val Opcode, h Handler) (Opcode, error) { return 0, myErr }
	if _, err := s.Walk([]byte(`"x"`), fail); !errors.Is(err, myErr) {
		tb.Errorf("custom error: got %v, want %v", err, myErr)
	}

	// 3. ErrBreak is a clean stop; traversal halts before recursing.
	n := 0
	brk := func(c *Applier, op, val Opcode, h Handler) (Opcode, error) {
		n++
		return val, ErrBreak
	}

	bs, _ := Compile([]byte(`{"type":"string","minLength":2}`))
	if d, err := bs.Walk([]byte(`"x"`), brk); err != nil || n != 1 || len(d) != 0 {
		tb.Errorf("ErrBreak: err=%v calls=%d diag=%d, want nil/1/0", err, n, len(d))
	}

	// 4. c.Fail records the verdict in a diag; ErrBreak is swallowed, so err is nil.
	rep := func(c *Applier, op, val Opcode, h Handler) (Opcode, error) {
		c.Fail(op, val, "handler says no")
		return val, ErrBreak
	}

	if d, err := s.Walk([]byte(`"x"`), rep); err != nil || len(d) != 1 || d[0].Message != "handler says no" {
		tb.Errorf("Fail: err=%v diag=%+v", err, d)
	}
}

func TestWalkRewrite(tb *testing.T) {
	delegate := func(c *Applier, op, val Opcode, h Handler) (Opcode, error) { return c.Apply(op, val, h) }

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

		out, _, err := s.WalkRewrite(nil, []byte(tc.in), delegate)
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

	fail := func(c *Applier, op, val Opcode, h Handler) (Opcode, error) { return 0, myErr }
	if _, _, err := s.WalkRewrite(nil, []byte(`{}`), fail); !errors.Is(err, myErr) {
		tb.Errorf("walkrewrite custom error: got %v, want %v", err, myErr)
	}
}

func TestWalkRead(tb *testing.T) {
	// A handler reads every scalar via the public Opcode/Buffer API while still
	// delegating, so validation is unchanged.
	got := map[string]bool{}

	var collect func(b BufferReader, val Opcode)
	collect = func(b BufferReader, val Opcode) {
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

	h := func(c *Applier, op, val Opcode, h Handler) (Opcode, error) {
		collect(c.Buf().Reader(), val)
		return c.Apply(op, val, h)
	}

	s, err := Compile([]byte(`{"type":"object"}`))
	if err != nil {
		tb.Fatal(err)
	}

	if _, err := s.Walk([]byte(`{"a":1,"b":["x",2]}`), h); err != nil {
		tb.Fatalf("walk: %v", err)
	}

	want := map[string]bool{`"a"`: true, "1": true, `"b"`: true, `"x"`: true, "2": true}
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

	h := func(c *Applier, op, val Opcode, h Handler) (Opcode, error) {
		if op.Op() == Properties {
			saw = true

			ns := c.SchemaBuf().Nodes(op)
			if len(ns) != 2*int(op.Arg()) {
				tb.Errorf("properties nodes: got %d words, want %d (arg=%d)", len(ns), 2*op.Arg(), op.Arg())
			}

			if got := string(c.SchemaBuf().Span(ns[0])); got != `"a"` {
				tb.Errorf("first property key: got %q, want %q", got, `"a"`)
			}
		}

		return c.Apply(op, val, h)
	}

	s, err := Compile([]byte(`{"properties":{"a":{"type":"integer"},"b":{"type":"string"}}}`))
	if err != nil {
		tb.Fatal(err)
	}

	if _, err := s.Walk([]byte(`{"a":1,"b":"x"}`), h); err != nil {
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

		diag, err := s.Validate([]byte(tc.data))
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

		diag, err := s.Validate([]byte(tc.data))
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

		diag, err := s.Validate([]byte(tc.data))
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

func TestXHook(tb *testing.T) {
	var rewriting bool

	// The x-type:upper keyword is now an inert Ext node; a Walk handler detects it
	// and uppercases the governed string value, replacing the old registered hook.
	upper := func(c *Applier, op, val Opcode, h Handler) (Opcode, error) {
		if op.Op() != Ext {
			return c.Apply(op, val, h)
		}

		rewriting = c.Rewriting()

		kids := c.SchemaBuf().Nodes(op)
		key := string(c.SchemaBuf().String(kids[0]))
		value := string(c.SchemaBuf().String(kids[1]))

		if key == "x-type" && value == "upper" && c.Rewriting() && val.Op() == String {
			return c.Buf().Writer().Span(String, bytes.ToUpper(c.Buf().Reader().Span(val))), nil
		}

		return val, nil
	}

	// 1. end-to-end rewrite via the Walk handler (no SetXHook).
	var s Schema

	if err := s.Compile([]byte(`{"properties":{"name":{"x-type":"upper"}}}`)); err != nil {
		tb.Fatalf("compile: %v", err)
	}

	out, _, err := s.WalkRewrite(nil, []byte(`{"name":"hi"}`), upper)
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

	if d, err := s.Walk([]byte(`{"name":"hi"}`), upper); err != nil || len(d) != 0 {
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

	if d, err := s.Validate([]byte(`{}`)); err != nil || len(d) != 0 {
		tb.Errorf("validate: err=%v diag=%v", err, d)
	}
}

func TestXTypeIDToObject(tb *testing.T) {
	// An x-type:id keyword rewrites the governed "entity/version" string into an
	// {"entity": <string>, "version": <int>} object; version is omitted when it is
	// 0 or absent. A sibling type:string check runs first (on the still-string
	// value), so it passes before the Ext swaps in the object.
	idToObject := func(c *Applier, op, val Opcode, h Handler) (Opcode, error) {
		if op.Op() != Ext {
			return c.Apply(op, val, h)
		}

		kids := c.SchemaBuf().Nodes(op)
		key := string(c.SchemaBuf().String(kids[0]))
		value := string(c.SchemaBuf().String(kids[1]))

		if key != "x-type" || value != "id" || !c.Rewriting() || val.Op() != String {
			return val, nil
		}

		// String() bytes are transient; string(...) copies them out.
		entity, ver, _ := strings.Cut(string(c.Buf().Reader().String(val)), "/")

		w := c.Buf().Writer()
		kv := []Opcode{w.Bytes([]byte("entity")), w.Bytes([]byte(entity))}

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

		out, diag, err := s.WalkRewrite(nil, []byte(tc.in), idToObject)
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

	h := func(c *Applier, op, val Opcode, h Handler) (Opcode, error) {
		if val.Op() == Number {
			return c.Buf().Writer().FromJSON([]byte(`{"wrapped":5}`))
		}

		return c.Apply(op, val, h)
	}

	out, _, err := s.WalkRewrite(nil, []byte(`5`), h)
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

	repl := func(c *Applier, op, val Opcode, h Handler) (Opcode, error) {
		if val.Op() == Number {
			return c.Buf().Writer().Span(Number, []byte("42")), nil
		}

		return c.Apply(op, val, h)
	}

	out, _, err := s.WalkRewrite(nil, []byte(`[1,2,3]`), repl)
	if err != nil {
		tb.Fatalf("walkrewrite: %v", err)
	}

	if got := string(out); got != `[42,42,42]` {
		tb.Errorf("walkrewrite: got %q, want %q", got, `[42,42,42]`)
	}

	// structural sharing: a pure delegate leaves the input byte-identical.
	pass := func(c *Applier, op, val Opcode, h Handler) (Opcode, error) { return c.Apply(op, val, h) }

	out, _, err = s.WalkRewrite(nil, []byte(`[1,2,3]`), pass)
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
		{`{}`, `"a"`, `"hi"`, String, `"hi"`},
		{`{"properties":{"a":{}}}`, `{"a":5,"b":7}`, `{"a":42,"b":7}`, Number, `42`}, // only governed scalar replaced
	} {
		s, err := Compile([]byte(tc.schema))
		if err != nil {
			tb.Errorf("compile %q: %v", tc.schema, err)
			continue
		}

		h := func(c *Applier, op, val Opcode, h Handler) (Opcode, error) {
			if val.Op() == tc.emit {
				return c.Buf().Writer().Span(tc.emit, []byte(tc.bytes)), nil
			}

			return c.Apply(op, val, h)
		}

		out, _, err := s.WalkRewrite(nil, []byte(tc.in), h)
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

	out, diag, err := s.Rewrite(nil, []byte(`{ "a" : 1 , "b" : [ 2, 3 ] }`))
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
	self := func(c *Applier, op, val Opcode, h Handler) (Opcode, error) {
		seen++
		return c.Apply(op, val, h)
	}

	var top int
	cut := func(c *Applier, op, val Opcode, h Handler) (Opcode, error) {
		top++
		return c.Apply(op, val, nil) // descendants fall to default; handler not re-entered
	}

	ds, es := s.Walk([]byte(`{"a":1}`), self)
	dc, ec := s.Walk([]byte(`{"a":1}`), cut)

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
	// produced from the tail — the snapshot/filter pattern Diags/SetDiags enable.
	s, err := Compile([]byte(`{"properties":{"a":{"type":"string"},"b":{"type":"string"}}}`))
	if err != nil {
		tb.Fatal(err)
	}

	// Suppress diagnostics raised anywhere under property "b".
	h := func(c *Applier, op, val Opcode, h Handler) (Opcode, error) {
		mark := len(c.Diags())

		nv, err := c.Apply(op, val, h)
		if err != nil {
			return nv, err
		}

		dp := c.DataPath()
		if len(dp) != 0 && string(c.Buf().Reader().String(dp[len(dp)-1])) == "b" {
			c.SetDiags(c.Diags()[:mark])
		}

		return nv, nil
	}

	d, err := s.Walk([]byte(`{"a":1,"b":2}`), h)
	if err != nil {
		tb.Fatal(err)
	}

	if len(d) != 1 {
		tb.Fatalf("diags=%d, want 1 (b suppressed, a kept)", len(d))
	}

	if d[0].Message != "wrong type" {
		tb.Errorf("message=%q", d[0].Message)
	}
}
