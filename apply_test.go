package schema

import (
	"errors"
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
	delegate := func(c Applier, op, val Opcode) (Opcode, error) { return c.Apply(op, val) }

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

		if (we == nil) != (ve == nil) || len(wd) != len(vd) {
			tb.Errorf("walk delegate %s vs validate: we=%v ve=%v wd=%d vd=%d", tc.data, we, ve, len(wd), len(vd))
		}

		if (we == nil) != tc.ok {
			tb.Errorf("walk %s against %s: ok=%v err=%v", tc.data, tc.schema, tc.ok, we)
		}
	}

	// 2. a custom error aborts the walk and propagates out.
	myErr := errors.New("boom")
	s, _ := Compile([]byte(`{"type":"string"}`))

	fail := func(c Applier, op, val Opcode) (Opcode, error) { return 0, myErr }
	if _, err := s.Walk([]byte(`"x"`), fail); !errors.Is(err, myErr) {
		tb.Errorf("custom error: got %v, want %v", err, myErr)
	}

	// 3. ErrBreak is a clean stop; traversal halts before recursing.
	n := 0
	brk := func(c Applier, op, val Opcode) (Opcode, error) {
		n++
		return val, ErrBreak
	}

	bs, _ := Compile([]byte(`{"type":"string","minLength":2}`))
	if d, err := bs.Walk([]byte(`"x"`), brk); err != nil || n != 1 || len(d) != 0 {
		tb.Errorf("ErrBreak: err=%v calls=%d diag=%d, want nil/1/0", err, n, len(d))
	}

	// 4. c.Fail adds a diag and the verdict is invalid.
	rep := func(c Applier, op, val Opcode) (Opcode, error) {
		c.Fail(op, val, "handler says no")
		return val, ErrBreak
	}

	if d, err := s.Walk([]byte(`"x"`), rep); !errors.Is(err, ErrInvalid) || len(d) != 1 || d[0].Msg != "handler says no" {
		tb.Errorf("Fail: err=%v diag=%+v", err, d)
	}
}

func TestWalkRewrite(tb *testing.T) {
	delegate := func(c Applier, op, val Opcode) (Opcode, error) { return c.Apply(op, val) }

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

	fail := func(c Applier, op, val Opcode) (Opcode, error) { return 0, myErr }
	if _, _, err := s.WalkRewrite(nil, []byte(`{}`), fail); !errors.Is(err, myErr) {
		tb.Errorf("walkrewrite custom error: got %v, want %v", err, myErr)
	}
}

func TestWalkRead(tb *testing.T) {
	// A handler reads every scalar via the public Opcode/Buffer API while still
	// delegating, so validation is unchanged.
	got := map[string]bool{}

	var collect func(b *Buffer, val Opcode)
	collect = func(b *Buffer, val Opcode) {
		switch val.Op() {
		case Num, Str:
			got[string(b.Span(val))] = true
		case Array:
			for _, e := range b.Nodes(val) {
				collect(b, e)
			}
		case Object:
			ns := b.Nodes(val)
			if len(ns) != 2*val.Arg() { // regression guard: Object Nodes returns 2n words
				tb.Errorf("object nodes: got %d words, want %d (arg=%d)", len(ns), 2*val.Arg(), val.Arg())
			}

			for i := 0; i < len(ns); i += 2 {
				collect(b, ns[i])   // key
				collect(b, ns[i+1]) // value
			}
		}
	}

	h := func(c Applier, op, val Opcode) (Opcode, error) {
		collect(c.Buf(), val)
		return c.Apply(op, val)
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

	h := func(c Applier, op, val Opcode) (Opcode, error) {
		if op.Op() == Properties {
			saw = true

			ns := c.SchemaBuf().Nodes(op)
			if len(ns) != 2*op.Arg() {
				tb.Errorf("properties nodes: got %d words, want %d (arg=%d)", len(ns), 2*op.Arg(), op.Arg())
			}

			if got := string(c.SchemaBuf().Span(ns[0])); got != `"a"` {
				tb.Errorf("first property key: got %q, want %q", got, `"a"`)
			}
		}

		return c.Apply(op, val)
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
