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
		d, err := s.Validate([]byte(tc.data))
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

	if d, err := s.Validate([]byte(`"x"`)); err != nil || len(d) != 0 {
		tb.Errorf("validate string: err=%v diag=%v", err, d)
	}

	if d, _ := s.Validate([]byte(`5`)); len(d) == 0 {
		tb.Errorf("validate number: want invalid")
	}
}

func TestDefsMerge(tb *testing.T) {
	// $defs and definitions (distinct keys) merge into one $defs block; both resolve.
	s, err := Compile([]byte(`{"properties":{"a":{"$ref":"#/$defs/A"},"b":{"$ref":"#/definitions/B"}},"$defs":{"A":{"type":"string"}},"definitions":{"B":{"type":"integer"}}}`))
	if err != nil {
		tb.Fatalf("compile: %v", err)
	}

	if d, err := s.Validate([]byte(`{"a":"x","b":1}`)); err != nil || len(d) != 0 {
		tb.Errorf("validate ok-case: err=%v diag=%v", err, d)
	}

	if d, _ := s.Validate([]byte(`{"a":1,"b":1}`)); len(d) == 0 {
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

	if d, err := s.Validate([]byte(`{"id":"x"}`)); err != nil || len(d) != 0 {
		tb.Errorf("validate ok: err=%v diag=%v", err, d)
	}

	if d, _ := s.Validate([]byte(`{"id":5}`)); len(d) == 0 {
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

	if d, err := w.Validate([]byte(`"x"`)); err != nil || len(d) != 0 {
		tb.Errorf("validate whole-doc ok: err=%v diag=%v", err, d)
	}

	if d, _ := w.Validate([]byte(`5`)); len(d) == 0 {
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

	if d, err := s.Validate([]byte(`{"id":"x"}`)); err != nil || len(d) != 0 {
		tb.Errorf("lazy validate ok: err=%v diag=%v", err, d)
	}

	if d, _ := s.Validate([]byte(`{"id":5}`)); len(d) == 0 {
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

	if d, err := s.Validate([]byte(`{"b":{"flag":true,"a":{"b":{}}}}`)); err != nil || len(d) != 0 {
		tb.Errorf("mutual ok (terminates one hop): err=%v diag=%v", err, d)
	}

	if d, _ := s.Validate([]byte(`{"b":{"flag":1}}`)); len(d) == 0 {
		tb.Errorf("mutual bad (flag not boolean): want invalid")
	}
}

func refNode(tb *testing.T, s *Schema, prop string) Opcode {
	tb.Helper()

	b := s.Reader()
	op := s.Root()

	if prop != "" {
		op = None

		for k, v := range b.Iter(b.Keyword(s.Root(), Properties)) {
			if string(b.String(k)) == prop {
				op = v
				break
			}
		}

		if op == None {
			tb.Fatalf("no property %q", prop)
		}
	}

	ref := b.Keyword(op, Ref)
	if ref == None {
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
	if node != None {
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

	_, err := s.Validate([]byte(`{}`))
	if !errors.Is(err, myErr) {
		tb.Errorf("resolve error: got %v, want %v", err, myErr)
	}
}
