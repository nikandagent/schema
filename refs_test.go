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
		_, err := s.Validate([]byte(tc.data))
		if (err == nil) != tc.ok {
			tb.Errorf("validate %s: ok=%v, err=%v", tc.data, tc.ok, err)
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

	if _, err := s.Validate([]byte(`"x"`)); err != nil {
		tb.Errorf("validate string: %v", err)
	}

	if _, err := s.Validate([]byte(`5`)); err == nil {
		tb.Errorf("validate number: want error")
	}
}

func TestDefsMerge(tb *testing.T) {
	// $defs and definitions (distinct keys) merge into one $defs block; both resolve.
	s, err := Compile([]byte(`{"properties":{"a":{"$ref":"#/$defs/A"},"b":{"$ref":"#/definitions/B"}},"$defs":{"A":{"type":"string"}},"definitions":{"B":{"type":"integer"}}}`))
	if err != nil {
		tb.Fatalf("compile: %v", err)
	}

	if _, err := s.Validate([]byte(`{"a":"x","b":1}`)); err != nil {
		tb.Errorf("validate ok-case: %v", err)
	}

	if _, err := s.Validate([]byte(`{"a":1,"b":1}`)); err == nil {
		tb.Errorf("validate bad A: want error")
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

	if _, err := s.Validate([]byte(`{"id":"x"}`)); err != nil {
		tb.Errorf("validate ok: %v", err)
	}

	if _, err := s.Validate([]byte(`{"id":5}`)); err == nil {
		tb.Errorf("validate bad: want error")
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

	if _, err := w.Validate([]byte(`"x"`)); err != nil {
		tb.Errorf("validate whole-doc ok: %v", err)
	}

	if _, err := w.Validate([]byte(`5`)); err == nil {
		tb.Errorf("validate whole-doc bad: want error")
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

	if _, err := s.Validate([]byte(`{"id":"x"}`)); err != nil {
		tb.Errorf("lazy validate ok: %v", err)
	}

	if _, err := s.Validate([]byte(`{"id":5}`)); err == nil {
		tb.Errorf("lazy validate bad: want error")
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

	if _, err := s.Validate([]byte(`{"b":{"flag":true,"a":{"b":{}}}}`)); err != nil {
		tb.Errorf("mutual ok (terminates one hop): %v", err)
	}

	if _, err := s.Validate([]byte(`{"b":{"flag":1}}`)); err == nil {
		tb.Errorf("mutual bad (flag not boolean): want error")
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
