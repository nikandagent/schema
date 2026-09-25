package schema

import (
	"errors"
	"strings"
	"testing"
)

func TestAnchor(tb *testing.T) {
	s, err := Compile([]byte(`{"properties":{"a":{"$anchor":"Foo","type":"integer"},"b":{"$ref":"#Foo"}}}`))
	if err != nil {
		tb.Fatalf("compile: %v", err)
	}

	for _, tc := range []struct {
		data string
		ok   bool
	}{
		{`{"b":5}`, true}, // #Foo resolves to the integer subschema
		{`{"b":"x"}`, false},
		{`{"a":"x"}`, false}, // anchored subschema still applies in place
	} {
		d, err := validate(s, []byte(tc.data))
		if err != nil {
			tb.Errorf("validate %s: unexpected error: %v", tc.data, err)
			continue
		}
		if (len(d) == 0) != tc.ok {
			tb.Errorf("validate %s: ok=%v, diag=%v", tc.data, tc.ok, d)
		}
	}

	got := string(s.Format(nil))
	if !strings.Contains(got, `"$anchor":"Foo"`) {
		tb.Errorf("format missing $anchor: %q", got)
	}

	if strings.Contains(got, `$defs`) {
		tb.Errorf("format has phantom $defs: %q", got)
	}
}

func TestDefsResolve(tb *testing.T) {
	s, err := Compile([]byte(`{"$defs":{"T":{"type":"string"}},"$ref":"#/$defs/T"}`))
	if err != nil {
		tb.Fatalf("compile: %v", err)
	}

	if d, err := validate(s, []byte(`"x"`)); err != nil || len(d) != 0 {
		tb.Errorf("validate string: err=%v diag=%v", err, d)
	}

	if d, _ := validate(s, []byte(`5`)); len(d) == 0 {
		tb.Errorf("validate number: want invalid")
	}
}

func TestDefsMerge(tb *testing.T) {
	// $defs and definitions (distinct keys) merge into one $defs block; both resolve.
	s, err := Compile([]byte(`{"properties":{"a":{"$ref":"#/$defs/A"},"b":{"$ref":"#/definitions/B"}},"$defs":{"A":{"type":"string"}},"definitions":{"B":{"type":"integer"}}}`))
	if err != nil {
		tb.Fatalf("compile: %v", err)
	}

	if d, err := validate(s, []byte(`{"a":"x","b":1}`)); err != nil || len(d) != 0 {
		tb.Errorf("validate ok-case: err=%v diag=%v", err, d)
	}

	if d, _ := validate(s, []byte(`{"a":1,"b":1}`)); len(d) == 0 {
		tb.Errorf("validate bad A: want invalid")
	}

	got := string(s.Format(nil))

	if !strings.Contains(got, `"$defs":{`) {
		tb.Errorf("format missing merged $defs: %q", got)
	}

	if strings.Contains(got, `"definitions"`) {
		tb.Errorf("format kept definitions: %q", got)
	}

	if !strings.Contains(got, `"A":{"type":"string"}`) || !strings.Contains(got, `"B":{"type":"integer"}`) {
		tb.Errorf("format missing merged keys: %q", got)
	}
}

func TestExternalAddDoc(tb *testing.T) {
	common, err := Compile([]byte(`{"$defs":{"Id":{"type":"string"}}}`))
	if err != nil {
		tb.Fatalf("compile common: %v", err)
	}

	var s Schema
	s.AddDoc("urn:objects:common", common)

	if err := s.Compile([]byte(`{"properties":{"id":{"$ref":"urn:objects:common#/$defs/Id"}}}`)); err != nil {
		tb.Fatalf("compile: %v", err)
	}

	if d, err := validate(&s, []byte(`{"id":"x"}`)); err != nil || len(d) != 0 {
		tb.Errorf("validate ok: err=%v diag=%v", err, d)
	}

	if d, _ := validate(&s, []byte(`{"id":5}`)); len(d) == 0 {
		tb.Errorf("validate bad: want invalid")
	}

	// whole-document external ref (no fragment) resolves to the doc root.
	leaf, err := Compile([]byte(`{"type":"string"}`))
	if err != nil {
		tb.Fatalf("compile leaf: %v", err)
	}

	var w Schema
	w.AddDoc("urn:objects:bbb", leaf)

	if err := w.Compile([]byte(`{"$ref":"urn:objects:bbb"}`)); err != nil {
		tb.Fatalf("compile whole-doc: %v", err)
	}

	if d, err := validate(&w, []byte(`"x"`)); err != nil || len(d) != 0 {
		tb.Errorf("validate whole-doc ok: err=%v diag=%v", err, d)
	}

	if d, _ := validate(&w, []byte(`5`)); len(d) == 0 {
		tb.Errorf("validate whole-doc bad: want invalid")
	}

	// unresolved external handle, no registry entry and no Resolve hook -> error.
	var u Schema
	if err := u.Compile([]byte(`{"$ref":"urn:objects:missing#/x"}`)); err == nil {
		tb.Errorf("compile unresolved: want error")
	}
}

func TestLazyResolve(tb *testing.T) {
	var s Schema
	s.Resolve = func(base, ref string) ([]byte, error) {
		if ref == "urn:objects:sib" {
			return []byte(`{"type":"string"}`), nil
		}

		return nil, errors.New("unknown " + ref)
	}

	if err := s.Compile([]byte(`{"properties":{"id":{"$ref":"urn:objects:sib#"}}}`)); err != nil {
		tb.Fatalf("compile: %v", err)
	}

	if d, err := validate(&s, []byte(`{"id":"x"}`)); err != nil || len(d) != 0 {
		tb.Errorf("lazy validate ok: err=%v diag=%v", err, d)
	}

	if d, _ := validate(&s, []byte(`{"id":5}`)); len(d) == 0 {
		tb.Errorf("lazy validate bad: want invalid")
	}
}

func TestMutualResolve(tb *testing.T) {
	aaa := `{"$id":"urn:objects:aaa","properties":{"b":{"$ref":"urn:objects:bbb#"}}}`
	bbb := `{"$id":"urn:objects:bbb","properties":{"a":{"$ref":"urn:objects:aaa#"},"flag":{"type":"boolean"}}}`

	var s Schema
	s.Resolve = func(base, ref string) ([]byte, error) {
		switch ref {
		case "urn:objects:aaa":
			return []byte(aaa), nil
		case "urn:objects:bbb":
			return []byte(bbb), nil
		}

		return nil, errors.New("unknown " + ref)
	}

	if err := s.Compile([]byte(aaa)); err != nil {
		tb.Fatalf("compile: %v", err)
	}

	if d, err := validate(&s, []byte(`{"b":{"flag":true,"a":{"b":{}}}}`)); err != nil || len(d) != 0 {
		tb.Errorf("mutual ok (terminates one hop): err=%v diag=%v", err, d)
	}

	if d, _ := validate(&s, []byte(`{"b":{"flag":1}}`)); len(d) == 0 {
		tb.Errorf("mutual bad (flag not boolean): want invalid")
	}
}

func refNode(tb *testing.T, s *Schema, prop string) Node {
	tb.Helper()

	b := s.Reader()
	op := s.Root()

	if prop != "" {
		op = Node{}

		for k, v := range b.Iter(b.Keyword(s.Root(), Properties)) {
			if string(b.String(k)) == prop {
				op = v
				break
			}
		}

		if op.Op() == None {
			tb.Fatalf("no property %q", prop)
		}
	}

	ref := b.Keyword(op, Ref)
	if ref.Op() == None {
		tb.Fatalf("no $ref in %q", prop)
	}

	return ref
}

func TestRefTarget(tb *testing.T) {
	for _, tc := range []struct {
		in   string
		prop string // property holding the $ref; empty for a root-level one
		want Types
	}{
		{`{"$defs":{"T":{"type":"string"}},"$ref":"#/$defs/T"}`, "", TypeString},
		{`{"$defs":{"a/b":{"type":"integer"}},"$ref":"#/$defs/a~1b"}`, "", TypeInteger},
		{`{"$defs":{"T":{"type":"number"}},"properties":{"a":{"$ref":"#/$defs/T"}}}`, "a", TypeNumber},
		{`{"properties":{"a":{"$anchor":"Foo","type":"integer"},"b":{"$ref":"#Foo"}}}`, "b", TypeInteger},
	} {
		s, err := Compile([]byte(tc.in))
		if err != nil {
			tb.Errorf("compile %q: %v", tc.in, err)
			continue
		}

		t, node, err := s.RefTarget(refNode(tb, s, tc.prop))
		if err != nil {
			tb.Errorf("reftarget %q: %v", tc.in, err)
			continue
		}

		if t != s {
			tb.Errorf("reftarget %q: got another document, want the same", tc.in)
			continue
		}

		if got := TypesOf(t.Reader().Keyword(node, Type)); got != tc.want {
			tb.Errorf("reftarget %q: target type %v, want %v", tc.in, got, tc.want)
		}
	}

	// "#" is the document root itself.
	{
		s, err := Compile([]byte(`{"properties":{"a":{"$ref":"#"}}}`))
		if err != nil {
			tb.Fatalf("compile root ref: %v", err)
		}

		t, node, err := s.RefTarget(refNode(tb, s, "a"))
		if err != nil {
			tb.Fatalf("reftarget root ref: %v", err)
		}

		if t != s || node != s.Root() {
			tb.Errorf("reftarget root ref: got %v, want root %v", node, s.Root())
		}
	}

	// Not a Ref: a program bug, like every other node reader.
	{
		s, err := Compile([]byte(`{"type":"string"}`))
		if err != nil {
			tb.Fatalf("compile: %v", err)
		}

		mustPanic(tb, "RefTarget(All)", func() { s.RefTarget(s.Root()) })
		mustPanic(tb, "RefTarget(Type)", func() { s.RefTarget(s.Reader().Keyword(s.Root(), Type)) })
	}
}

func TestRefTargetExternal(tb *testing.T) {
	common, err := Compile([]byte(`{"$defs":{"Id":{"type":"string"}}}`))
	if err != nil {
		tb.Fatalf("compile common: %v", err)
	}

	var s Schema
	s.AddDoc("urn:objects:common", common)

	if err := s.Compile([]byte(`{"properties":{"id":{"$ref":"urn:objects:common#/$defs/Id"}}}`)); err != nil {
		tb.Fatalf("compile: %v", err)
	}

	t, node, err := s.RefTarget(refNode(tb, &s, "id"))
	if err != nil {
		tb.Fatalf("reftarget: %v", err)
	}

	if t != common {
		tb.Fatalf("reftarget: got doc %p, want the registered one %p", t, common)
	}

	// The node belongs to the other document, so it reads through its Reader.
	if got := TypesOf(t.Reader().Keyword(node, Type)); got != TypeString {
		tb.Errorf("reftarget: target type %v, want %v", got, TypeString)
	}
}

func TestRefTargetUnresolved(tb *testing.T) {
	var s Schema
	s.Resolve = func(base, ref string) ([]byte, error) { return []byte(`{"type":"string"}`), nil }

	// The document loads, the fragment is not in it; compile defers both to apply.
	if err := s.Compile([]byte(`{"$ref":"urn:x#/$defs/missing"}`)); err != nil {
		tb.Fatalf("compile: %v", err)
	}

	_, node, err := s.RefTarget(refNode(tb, &s, ""))
	if !errors.Is(err, ErrRef) {
		tb.Errorf("reftarget: err %v, want Is(ErrRef)", err)
	}
	if node.Op() != None {
		tb.Errorf("reftarget: node %v, want None", node)
	}
}

func TestResolveError(tb *testing.T) {
	myErr := errors.New("boom")

	var s Schema
	s.Resolve = func(base, ref string) ([]byte, error) { return nil, myErr }

	if err := s.Compile([]byte(`{"$ref":"urn:x#"}`)); err != nil {
		tb.Fatalf("compile (resolve deferred to apply): %v", err)
	}

	_, err := validate(&s, []byte(`{}`))
	if !errors.Is(err, myErr) {
		tb.Errorf("resolve error: got %v, want %v", err, myErr)
	}
}

func TestResolveTransitive(tb *testing.T) {
	var calls [][2]string

	var s Schema
	s.ID = "urn:a"
	s.Resolve = func(base, ref string) ([]byte, error) {
		calls = append(calls, [2]string{base, ref})

		switch ref {
		case "urn:b":
			return []byte(`{"properties":{"c":{"$ref":"urn:c#"}}}`), nil
		case "urn:c":
			return []byte(`{"type":"integer"}`), nil
		}

		return nil, errors.New("unknown " + ref)
	}

	if err := s.Compile([]byte(`{"properties":{"b":{"$ref":"urn:b#"}}}`)); err != nil {
		tb.Fatalf("compile: %v", err)
	}

	if d, err := validate(&s, []byte(`{"b":{"c":5}}`)); err != nil || len(d) != 0 {
		tb.Fatalf("validate ok: err=%v diag=%v", err, d)
	}

	if d, _ := validate(&s, []byte(`{"b":{"c":"x"}}`)); len(d) == 0 {
		tb.Errorf("validate bad: want invalid")
	}

	want := [][2]string{{"urn:a", "urn:b"}, {"urn:b", "urn:c"}}
	if len(calls) != len(want) {
		tb.Fatalf("resolve calls %v, want %v", calls, want)
	}

	for i, w := range want {
		if calls[i] != w {
			tb.Errorf("resolve call %d: %v, want %v", i, calls[i], w)
		}
	}
}

func TestIDBase(tb *testing.T) {
	for _, tc := range []struct {
		id, text, want string
	}{
		{"urn:root", `{"$ref":"urn:sib#"}`, "urn:root"},
		{"urn:provided", `{"$id":"urn:from-text","$ref":"urn:sib#"}`, "urn:from-text"},
		{"", `{"$id":"urn:from-text","$ref":"urn:sib#"}`, "urn:from-text"},
	} {
		var base string

		var s Schema
		s.ID = tc.id
		s.Resolve = func(b, ref string) ([]byte, error) {
			base = b
			return []byte(`{"type":"string"}`), nil
		}

		if err := s.Compile([]byte(tc.text)); err != nil {
			tb.Errorf("compile %q: %v", tc.text, err)
			continue
		}

		if s.ID != tc.want {
			tb.Errorf("compile %q with ID %q: ID=%q, want %q", tc.text, tc.id, s.ID, tc.want)
		}

		if d, err := validate(&s, []byte(`"x"`)); err != nil || len(d) != 0 {
			tb.Errorf("validate %q: err=%v diag=%v", tc.text, err, d)
			continue
		}

		if base != tc.want {
			tb.Errorf("resolve base %q, want %q", base, tc.want)
		}
	}
}

func TestIDSelfRegistered(tb *testing.T) {
	for _, tc := range []struct {
		id, text string
		hook     bool
	}{
		{"urn:root", `{"$defs":{"X":{"type":"integer"}},"properties":{"a":{"$ref":"urn:root#/$defs/X"}}}`, true},
		{"urn:root", `{"$defs":{"X":{"type":"integer"}},"properties":{"a":{"$ref":"urn:root#/$defs/X"}}}`, false},
		{"", `{"$id":"urn:self","$defs":{"X":{"type":"integer"}},"properties":{"a":{"$ref":"urn:self#/$defs/X"}}}`, false},
		{"urn:provided", `{"$id":"urn:self","$defs":{"X":{"type":"integer"}},"properties":{"a":{"$ref":"urn:self#/$defs/X"}}}`, false},
	} {
		var s Schema
		s.ID = tc.id

		if tc.hook {
			s.Resolve = func(base, ref string) ([]byte, error) {
				tb.Errorf("Resolve called for a self-ref: base=%q ref=%q", base, ref)
				return nil, errors.New("unexpected")
			}
		}

		if err := s.Compile([]byte(tc.text)); err != nil {
			tb.Errorf("compile %q with ID %q: %v", tc.text, tc.id, err)
			continue
		}

		if d, err := validate(&s, []byte(`{"a":5}`)); err != nil || len(d) != 0 {
			tb.Errorf("validate ok %q: err=%v diag=%v", tc.text, err, d)
		}

		if d, _ := validate(&s, []byte(`{"a":"x"}`)); len(d) == 0 {
			tb.Errorf("validate bad %q: want invalid", tc.text)
		}
	}
}

func TestAddDocNames(tb *testing.T) {
	doc, err := Compile([]byte(`{"type":"string"}`))
	if err != nil {
		tb.Fatal(err)
	}

	var parent Schema
	parent.AddDoc("urn:x", doc)

	if doc.ID != "urn:x" {
		tb.Errorf("doc.ID=%q, want %q", doc.ID, "urn:x")
	}
}

func TestNoIDInternalRefs(tb *testing.T) {
	s, err := Compile([]byte(`{"$defs":{"X":{"type":"integer"}},"properties":{"a":{"$ref":"#/$defs/X"},"b":{"$ref":"#"}}}`))
	if err != nil {
		tb.Fatalf("compile: %v", err)
	}

	if s.ID != "" {
		tb.Errorf("ID=%q, want empty", s.ID)
	}

	if d, err := validate(s, []byte(`{"a":5}`)); err != nil || len(d) != 0 {
		tb.Errorf("validate ok: err=%v diag=%v", err, d)
	}

	if d, _ := validate(s, []byte(`{"a":"x"}`)); len(d) == 0 {
		tb.Errorf("validate bad: want invalid")
	}
}

// TestSubschemaID pins that a subschema carrying $id is registered under that
// URI, so a $ref to it resolves inside the same document with no resolver in
// sight — the bare URI, and a pointer into it.
func TestSubschemaID(tb *testing.T) {
	s, err := Compile([]byte(`{"$defs":{"T":{"$id":"urn:acme:t","type":"integer"}},"properties":{"a":{"$ref":"urn:acme:t"}}}`))
	if err != nil {
		tb.Fatalf("compile: %v", err)
	}

	for _, tc := range []struct {
		data string
		ok   bool
	}{
		{`{"a":5}`, true},
		{`{"a":"x"}`, false},
	} {
		d, err := validate(s, []byte(tc.data))
		if err != nil {
			tb.Errorf("validate %s: %v", tc.data, err)
			continue
		}

		if (len(d) == 0) != tc.ok {
			tb.Errorf("validate %s: ok=%v diag=%v", tc.data, tc.ok, d)
		}
	}

	t, node, err := s.Lookup("urn:acme:t")
	if err != nil {
		tb.Fatalf("lookup: %v", err)
	}

	if t != s {
		tb.Errorf("lookup: got another document, want the same")
	}

	if got := string(t.FormatNode(nil, node)); got != `{"$id":"urn:acme:t","type":"integer"}` {
		tb.Errorf("lookup: got %s", got)
	}

	// a property subschema names itself just as well
	p, err := Compile([]byte(`{"properties":{"x":{"$id":"urn:acme:x","type":"string"},"y":{"$ref":"urn:acme:x"}}}`))
	if err != nil {
		tb.Fatalf("compile property $id: %v", err)
	}

	if d, err := validate(p, []byte(`{"x":"a","y":"b"}`)); err != nil || len(d) != 0 {
		tb.Errorf("validate ok: err=%v diag=%v", err, d)
	}

	if d, _ := validate(p, []byte(`{"y":5}`)); len(d) == 0 {
		tb.Errorf("validate bad: want invalid")
	}
}

// TestSubschemaIDPointer walks a JSON Pointer from a self-named schema, at any
// nesting depth, and from the root's own $id.
func TestSubschemaIDPointer(tb *testing.T) {
	src := `{"$id":"urn:root","properties":{"a":{"$id":"urn:inner","properties":{"b":{"minLength":2}}}}}`

	s, err := Compile([]byte(src))
	if err != nil {
		tb.Fatalf("compile: %v", err)
	}

	inner := s.Reader().Find(s.Reader().Keyword(s.Root(), Properties), "a")

	for _, tc := range []struct {
		ref  string
		want string
	}{
		{"urn:root", src},
		{"urn:inner", `{"$id":"urn:inner","properties":{"b":{"minLength":2}}}`},
		{"urn:inner#/properties/b", `{"minLength":2}`},
		{"urn:root#/properties/a", `{"$id":"urn:inner","properties":{"b":{"minLength":2}}}`},
		{"urn:root#/properties/a/properties/b", `{"minLength":2}`},
		{"#/properties/a/properties/b", `{"minLength":2}`},
	} {
		t, node, err := s.Lookup(tc.ref)
		if err != nil {
			tb.Errorf("lookup %q: %v", tc.ref, err)
			continue
		}

		if t != s {
			tb.Errorf("lookup %q: got another document, want the same", tc.ref)
			continue
		}

		if got := string(t.FormatNode(nil, node)); got != tc.want {
			tb.Errorf("lookup %q: got %s, want %s", tc.ref, got, tc.want)
		}
	}

	// the two URIs name different nodes
	if _, root, _ := s.Lookup("urn:root"); root != s.Root() {
		tb.Errorf("urn:root does not name the root")
	}

	if _, node, _ := s.Lookup("urn:inner"); node != inner {
		tb.Errorf("urn:inner does not name the inner subschema")
	}

	for _, ref := range []string{"urn:inner#/properties/zz", "urn:root#/properties/zz", "urn:nope"} {
		if _, _, err := s.Lookup(ref); !errors.Is(err, ErrRef) {
			tb.Errorf("lookup %q: err %v, want Is(ErrRef)", ref, err)
		}
	}

	// a $ref written in the schema resolves the same way, with no resolver
	var w Schema

	if err := w.Compile([]byte(`{"properties":{"x":{"$id":"urn:acme:x","properties":{"y":{"type":"string"}}},"z":{"$ref":"urn:acme:x#/properties/y"}}}`)); err != nil {
		tb.Fatalf("compile pointer ref: %v", err)
	}

	if d, err := validate(&w, []byte(`{"z":"ok"}`)); err != nil || len(d) != 0 {
		tb.Errorf("validate ok: err=%v diag=%v", err, d)
	}

	if d, _ := validate(&w, []byte(`{"z":5}`)); len(d) == 0 {
		tb.Errorf("validate bad: want invalid")
	}
}

// TestIDKeyword pins $id as a keyword: reachable with Keyword, readable, inert
// at apply, and round-tripping through Format.
func TestIDKeyword(tb *testing.T) {
	src := `{"$id":"urn:acme:doc","type":"object","properties":{"a":{"$id":"urn:acme:a","type":"string"}}}`

	s, err := Compile([]byte(src))
	if err != nil {
		tb.Fatalf("compile: %v", err)
	}

	r := s.Reader()

	id := r.Keyword(s.Root(), ID)
	if id.Op() != ID {
		tb.Fatalf("Keyword(root, ID): got %v", id.Op())
	}

	if got := string(r.String(id)); got != "urn:acme:doc" {
		tb.Errorf("$id value: got %q", got)
	}

	if got := id.Keyword(); got != "$id" {
		tb.Errorf("$id Keyword(): got %q", got)
	}

	if got := string(s.FormatKeyword(nil, id)); got != `"urn:acme:doc"` {
		tb.Errorf("FormatKeyword($id): got %s", got)
	}

	off, end, ok := id.Src()
	if !ok || src[off:end] != `"$id":"urn:acme:doc"` {
		tb.Errorf("$id src: %d:%d ok=%v %q", off, end, ok, src[off:end])
	}

	// $id sorts first, and both levels round-trip
	if got := string(s.Format(nil)); got != src {
		tb.Errorf("format: got %s, want %s", got, src)
	}

	if got := string(s.Format(nil)); got[:len(`{"$id":`)] != `{"$id":` {
		tb.Errorf("format does not open with $id: %s", got)
	}

	// inert at apply: nothing about the document is judged by its name
	if d, err := validate(s, []byte(`{"a":"x"}`)); err != nil || len(d) != 0 {
		tb.Errorf("validate: err=%v diag=%v", err, d)
	}

	// authored out of order, it still comes back first
	o, err := Compile([]byte(`{"type":"string","$id":"urn:acme:o"}`))
	if err != nil {
		tb.Fatalf("compile reordered: %v", err)
	}

	if got := string(o.Format(nil)); got != `{"$id":"urn:acme:o","type":"string"}` {
		tb.Errorf("format reordered: got %s", got)
	}

	// and it is no longer an unknown keyword under strict flags
	strict := Schema{Flags: SchemaRejectUnknown | SchemaRejectUnsupported}
	if err := strict.Compile([]byte(`{"$id":"urn:acme:s"}`)); err != nil {
		tb.Errorf("compile strict: %v", err)
	}

	if err := strict.Compile([]byte(`{"$id":5}`)); !errors.Is(err, ErrKeyword) {
		tb.Errorf("compile $id:5: err %v, want Is(ErrKeyword)", err)
	}
}

func TestDuplicateID(tb *testing.T) {
	for _, in := range []string{
		`{"$defs":{"A":{"$id":"urn:a"},"B":{"$id":"urn:a"}}}`,
		`{"properties":{"a":{"$id":"urn:a"},"b":{"$id":"urn:a"}}}`,
		`{"$defs":{"A":{"$id":"urn:a","properties":{"inner":{"$id":"urn:a"}}}}}`,
		// the root is registered like any other self-named schema, so a
		// subschema may not take its name either
		`{"$id":"urn:a","properties":{"x":{"$id":"urn:a"}}}`,
	} {
		var s Schema

		err := s.Compile([]byte(in))

		d := AsDiag(err)
		if len(d) != 1 || d[0].Code != DuplicateID {
			tb.Errorf("compile %s: %v (%+v), want DuplicateID", in, err, d)
			continue
		}

		if !errors.Is(err, ErrRef) {
			tb.Errorf("compile %s: err %v, want Is(ErrRef)", in, err)
		}

		off, end := d[0].opSpan()
		if got := in[off:end]; got != `"$id":"urn:a"` {
			tb.Errorf("compile %s: span %d:%d %q, want the $id pair", in, off, end, got)
		}
	}

	// two anchors still report DuplicateAnchor, a separate code
	var a Schema

	d := AsDiag(a.Compile([]byte(`{"$anchor":"A","$defs":{"T":{"$anchor":"A"}}}`)))
	if len(d) != 1 || d[0].Code != DuplicateAnchor {
		tb.Errorf("duplicate anchor: %+v, want DuplicateAnchor", d)
	}
}
