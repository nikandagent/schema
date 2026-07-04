package schema

import (
	"errors"
	"sort"
	"strings"
	"testing"
)

// found renders each diag as "message@<located source>" so a test can assert the
// set of findings (message + where it points) order-independently.
func found(doc []byte, d []Diag) []string {
	out := make([]string, 0, len(d))
	for _, x := range d {
		out = append(out, x.Message+"@"+string(doc[x.Off:x.End]))
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

// TestAt is the behavior spec for the Walk At(path) option: it scopes validation
// to the value(s) at a data path, skipping ancestor/sibling constraints.
func TestAt(tb *testing.T) {
	// object with a typed array of objects, plus a scalar and a root required.
	sc := mustCompile(tb, `{
		"properties": {
			"users": {"type":"array","items":{"properties":{
				"name":{"type":"string"},
				"age":{"type":"integer"}
			}}},
			"title": {"type":"string"}
		},
		"required": ["missing"]
	}`)
	doc := []byte(`{"users":[{"name":"ok","age":5},{"name":42,"age":"bad"}],"title":99}`)

	run := func(name string, opts []Option, want ...string) {
		d, err := sc.Walk(doc, nil, opts...)
		if err != nil {
			tb.Errorf("%s: unexpected error: %v", name, err)
			return
		}
		wantSet(tb, name, doc, d, want...)
	}

	// whole doc: root required + both wrong scalars.
	run("whole", nil,
		"missing required property@"+string(doc),
		"wrong type@42", "wrong type@\"bad\"", "wrong type@99")

	// scalar leaf: only that value, ancestors skipped.
	run("title", []Option{At("title")}, "wrong type@99")

	// concrete index into the array.
	run("users[1].name", []Option{At("users", 1, "name")}, "wrong type@42")

	// negative index counts from the end.
	run("users[-1].name", []Option{At("users", -1, "name")}, "wrong type@42")

	// Each fans out over every element; only the bad ones report.
	run("users[].name", []Option{At("users", Each, "name")}, "wrong type@42")
	run("users[].age", []Option{At("users", Each, "age")}, "wrong type@\"bad\"")

	// a valid leaf produces nothing (and still no ancestor required).
	run("users[0].name", []Option{At("users", 0, "name")})

	// []byte key works like a string key.
	run("title as bytes", []Option{At([]byte("title"))}, "wrong type@99")
}

// TestAtUnresolved: a path that doesn't resolve (missing key, out-of-range index,
// wrong-kind step) scopes to no value — no diags, no error — matching how an
// unscoped walk never visits a location that isn't there.
func TestAtUnresolved(tb *testing.T) {
	// "n" is invalid in the doc, so an unscoped walk would report it — scoping to
	// an unresolvable path must suppress everything.
	sc := mustCompile(tb, `{"properties":{"users":{"type":"array","items":{"type":"integer"}},"n":{"type":"integer"}}}`)
	doc := []byte(`{"users":[1,2,3],"n":"wrong"}`)

	for _, opts := range [][]Option{
		{At("nope")},             // missing property
		{At("users", 99)},        // index out of range
		{At("users", 0, "deep")}, // descend past a scalar
	} {
		d, err := sc.Walk(doc, nil, opts...)
		if err != nil {
			tb.Errorf("%v: unexpected error: %v", opts, err)
		}
		if len(d) != 0 {
			tb.Errorf("%v: diags = %v, want none", opts, found(doc, d))
		}
	}
}

// wantMismatch asserts a single kind-mismatch diag whose location is
// out-of-document (0,0) — the offending At segment has no source position.
func wantMismatch(tb *testing.T, d []Diag, msg string) {
	tb.Helper()
	if len(d) != 1 || d[0].Message != msg {
		tb.Fatalf("diags = %v, want one %q", d, msg)
	}
	if d[0].Off != 0 || d[0].End != 0 {
		tb.Errorf("location = [%d,%d], want out-of-document (0,0)", d[0].Off, d[0].End)
	}
}

// TestAtKeyIntoArray: a string step where the schema/value is an array is a
// kind mismatch (you'd be assigning arr.key) — a conformance diag, unlike an
// out-of-range index which just resolves to nothing.
func TestAtKeyIntoArray(tb *testing.T) {
	sc := mustCompile(tb, `{"properties":{"users":{"type":"array","items":{"type":"integer"}}}}`)
	doc := []byte(`{"users":[1,2,3]}`)

	d, err := sc.Walk(doc, nil, At("users", "oops"))
	if err != nil {
		tb.Fatalf("unexpected error: %v", err)
	}
	wantMismatch(tb, d, "schema is array, val supposes object")
}

// TestAtIndexIntoObject is the mirror: an int step where the schema is an object
// is a kind mismatch (you'd be indexing an object).
func TestAtIndexIntoObject(tb *testing.T) {
	sc := mustCompile(tb, `{"properties":{"a":{"type":"integer"}}}`)
	doc := []byte(`{"a":1}`)

	d, err := sc.Walk(doc, nil, At(0))
	if err != nil {
		tb.Fatalf("unexpected error: %v", err)
	}
	wantMismatch(tb, d, "schema is object, val supposes array")
}

// TestAtConformance: assigning a value whose shape contradicts the schema is a
// validation finding. Here the schema says users is an array but the doc has it
// as an object (as you'd get assigning users.oops) — a wrong-type diag.
func TestAtConformance(tb *testing.T) {
	// root required makes an unscoped walk noisy; scoping must report only the
	// users type conflict.
	sc := mustCompile(tb, `{"properties":{"users":{"type":"array","items":{"type":"integer"}}},"required":["zzz"]}`)
	doc := []byte(`{"users":{"oops":1}}`)

	d, err := sc.Walk(doc, nil, At("users", "oops"))
	if err != nil {
		tb.Fatalf("unexpected error: %v", err)
	}
	wantSet(tb, "array-as-object", doc, d, "wrong type@"+`{"oops":1}`)
}

// TestAtPatternProps: At a key governed only by patternProperties validates it
// against the matching pattern subschema.
func TestAtPatternProps(tb *testing.T) {
	sc := mustCompile(tb, `{"patternProperties":{"^x":{"type":"number"}}}`)
	doc := []byte(`{"xa":"str","yb":true}`)

	d, err := sc.Walk(doc, nil, At("xa"))
	if err != nil {
		tb.Fatalf("unexpected error: %v", err)
	}
	wantSet(tb, "pattern key", doc, d, "wrong type@\"str\"")

	// a key matched by no pattern is unconstrained -> no diags.
	d, _ = sc.Walk(doc, nil, At("yb"))
	if len(d) != 0 {
		tb.Errorf("unmatched key: diags = %v, want none", found(doc, d))
	}
}

// TestAtAdditionalFalse: At a key forbidden by additionalProperties:false is a
// finding (the assignment isn't allowed); a permitted key validates its value.
func TestAtAdditionalFalse(tb *testing.T) {
	sc := mustCompile(tb, `{"properties":{"a":{"type":"integer"}},"additionalProperties":false}`)
	doc := []byte(`{"a":1,"b":"x"}`)

	d, err := sc.Walk(doc, nil, At("b"))
	if err != nil {
		tb.Fatalf("unexpected error: %v", err)
	}
	if len(d) == 0 {
		tb.Errorf("At forbidden key: want a diag, got none")
	}

	// a declared property still validates against its own subschema.
	d, _ = sc.Walk([]byte(`{"a":"nope"}`), nil, At("a"))
	wantSet(tb, "declared key", []byte(`{"a":"nope"}`), d, "wrong type@\"nope\"")
}

// TestAtBadKey: a path step that is neither string/[]byte, int, nor Each is a
// caller mistake surfaced as an error from the option.
func TestAtBadKey(tb *testing.T) {
	sc := mustCompile(tb, `{"properties":{"a":{"type":"string"}}}`)

	_, err := sc.Walk([]byte(`{"a":"x"}`), nil, At("a", 3.14))
	if err == nil {
		tb.Errorf("At(float): want error, got nil")
	}
	var e *Error
	if !errors.As(err, &e) {
		tb.Errorf("At(float): err %v is not *Error", err)
	}
}

// deepSchema/deepDoc: object -> teams[] -> object -> members[] -> object with a
// string name and a roles[] of strings. Two bad leaves are planted: a numeric
// name (7) and a numeric role (9).
const deepSchema = `{"properties":{"org":{"properties":{"teams":{"type":"array","items":{
	"properties":{"members":{"type":"array","items":{
		"properties":{"name":{"type":"string"},"roles":{"type":"array","items":{"type":"string"}}}
	}}}
}}}}}}`

const deepDoc = `{"org":{"teams":[` +
	`{"members":[{"name":"a","roles":["x"]},{"name":7,"roles":["y",9]}]},` +
	`{"members":[{"name":"b","roles":[]}]}` +
	`]}}`

// TestAtDeep drills scoped paths several levels into nested arrays and objects,
// mixing concrete indices, a negative index, and Each wildcards.
func TestAtDeep(tb *testing.T) {
	sc := mustCompile(tb, deepSchema)
	doc := []byte(deepDoc)

	run := func(name string, opts []Option, want ...string) {
		d, err := sc.Walk(doc, nil, opts...)
		if err != nil {
			tb.Errorf("%s: unexpected error: %v", name, err)
			return
		}
		wantSet(tb, name, doc, d, want...)
	}

	// whole doc: both bad leaves surface.
	run("whole", nil, "wrong type@7", "wrong type@9")

	// concrete path to the numeric name.
	run("name", []Option{At("org", "teams", 0, "members", 1, "name")}, "wrong type@7")

	// concrete path to the numeric role, two arrays deep.
	run("role", []Option{At("org", "teams", 0, "members", 1, "roles", 1)}, "wrong type@9")

	// a valid leaf reports nothing.
	run("good name", []Option{At("org", "teams", 0, "members", 0, "name")})

	// negative index into the outer array, then a valid leaf -> nothing.
	run("last team", []Option{At("org", "teams", -1, "members", 0, "name")})

	// Each fans out over every team and member; only the numeric name shows.
	run("each name", []Option{At("org", "teams", Each, "members", Each, "name")}, "wrong type@7")

	// Each all the way down to roles.
	run("each role", []Option{At("org", "teams", Each, "members", Each, "roles", Each)}, "wrong type@9")
}

// TestAtSkipsRequired: scoping to a leaf skips ancestor required constraints,
// which is the point of the assign-a-leaf use case.
func TestAtSkipsRequired(tb *testing.T) {
	sc := mustCompile(tb, `{"properties":{"a":{"properties":{"b":{"type":"string"}},"required":["b"]}}}`)
	doc := []byte(`{"a":{"c":5}}`)

	// unscoped: the missing required property is reported.
	d, err := sc.Walk(doc, nil)
	if err != nil {
		tb.Fatalf("unscoped: %v", err)
	}
	wantSet(tb, "unscoped", doc, d, "missing required property@"+`{"c":5}`)

	// scoped to a sibling leaf: required is skipped, and c is unconstrained.
	d, err = sc.Walk(doc, nil, At("a", "c"))
	if err != nil {
		tb.Fatalf("scoped: %v", err)
	}
	if len(d) != 0 {
		tb.Errorf("scoped: diags = %v, want none", found(doc, d))
	}
}
