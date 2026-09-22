package schema

import (
	"errors"
	"testing"
)

const lookupSrc = `{
	"$defs":{"T":{"type":"string"},"a/b":{"type":"integer"},"t~k":{"type":"null"}},
	"properties":{
		"user":{
			"type":"array",
			"items":{"type":"number"},
			"not":{"type":"null"},
			"if":{"type":"string"},"then":{"minLength":1},"else":{"type":"integer"},
			"additionalProperties":{"type":"boolean"}
		},
		"a/b":{"type":"boolean"},
		"anc":{"$anchor":"Anchor","type":"integer"}
	},
	"patternProperties":{"^x":{"type":"string"}},
	"additionalProperties":{"type":"null"},
	"allOf":[{"type":"object"},{"type":"array"}],
	"anyOf":[{"type":"string"}],
	"oneOf":[{"type":"integer"}],
	"prefixItems":[{"type":"string"},{"type":"integer"}],
	"items":{"type":"boolean"},
	"enum":[1,2],
	"default":{"x":1},
	"x-ext":{"a":1},
	"title":"t"
}`

func TestLookup(tb *testing.T) {
	s, err := Compile([]byte(lookupSrc))
	if err != nil {
		tb.Fatalf("compile: %v", err)
	}

	root := string(s.Format(nil))

	for _, tc := range []struct {
		ref, want string
	}{
		{"", root},
		{"#", root},
		{"#/$defs/T", `{"type":"string"}`},
		{"#/$defs/a~1b", `{"type":"integer"}`},
		{"#/$defs/t~0k", `{"type":"null"}`},
		{"#Anchor", `{"type":"integer","$anchor":"Anchor"}`},

		{"#/properties/user/items", `{"type":"number"}`},
		{"#/properties/user/not", `{"type":"null"}`},
		{"#/properties/user/if", `{"type":"string"}`},
		{"#/properties/user/then", `{"minLength":1}`},
		{"#/properties/user/else", `{"type":"integer"}`},
		{"#/properties/user/additionalProperties", `{"type":"boolean"}`},
		{"#/properties/a~1b", `{"type":"boolean"}`},
		{"#/properties/anc", `{"type":"integer","$anchor":"Anchor"}`},
		{"#/prefixItems/1", `{"type":"integer"}`},

		{"#/allOf/1", `{"type":"array"}`},
		{"#/anyOf/0", `{"type":"string"}`},
		{"#/oneOf/0", `{"type":"integer"}`},
		{"#/items", `{"type":"boolean"}`},
		{"#/additionalProperties", `{"type":"null"}`},
		{"#/patternProperties/^x", `{"type":"string"}`},
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

	t, node, err := s.Lookup("#/properties/user")
	if err != nil || t != s {
		tb.Fatalf("lookup user: err=%v", err)
	}

	if got := TypesOf(t.Reader().Keyword(node, Type)); got != TypeArray {
		tb.Errorf("lookup user: type %v, want %v", got, TypeArray)
	}
}

func TestLookupNotFound(tb *testing.T) {
	s, err := Compile([]byte(lookupSrc))
	if err != nil {
		tb.Fatalf("compile: %v", err)
	}

	for _, ref := range []string{
		"#/nope",
		"#/properties",
		"#/$defs",
		"#/$defs/nope",
		"#/enum/0",
		"#/default",
		"#/title",
		"#/properties/user/type",
		"#/allOf/01",
		"#/allOf/2",
		"#/allOf/x",
		"#/allOf/",
		"#/prefixItems/2",
		"#foo",
		"#/x-ext/a",
		"#/",
		"#//",
		"nope",
	} {
		t, node, err := s.Lookup(ref)
		if err == nil {
			tb.Errorf("lookup %q: got %s, want error", ref, t.FormatNode(nil, node))
			continue
		}

		if !errors.Is(err, ErrRef) {
			tb.Errorf("lookup %q: err %v, want Is(ErrRef)", ref, err)
		}

		d := AsDiag(err)
		if len(d) != 1 {
			tb.Errorf("lookup %q: err %v (%T) is not a one-element Diagnostics", ref, err, err)
			continue
		}

		if d[0].Code != UnresolvedRef && d[0].Code != NoResolver || d[0].Off != 0 || d[0].End != 0 {
			tb.Errorf("lookup %q: diag %+v, want UnresolvedRef at 0:0", ref, d[0])
		}

		if node != None {
			tb.Errorf("lookup %q: node %v, want None", ref, node)
		}
	}
}

func TestLookupExternal(tb *testing.T) {
	x, err := Compile([]byte(`{"properties":{"a":{"type":"string"}}}`))
	if err != nil {
		tb.Fatalf("compile x: %v", err)
	}

	var s Schema
	s.AddDoc("urn:x", x)

	if err := s.Compile([]byte(`{}`)); err != nil {
		tb.Fatalf("compile: %v", err)
	}

	var a Applier

	t, node, err := s.Lookup("urn:x#/properties/a")
	if err != nil {
		tb.Fatalf("lookup: %v", err)
	}

	if t != x || t == &s {
		tb.Fatalf("lookup: got doc %p, want the registered %p", t, x)
	}

	if got := string(t.FormatNode(nil, node)); got != `{"type":"string"}` {
		tb.Errorf("lookup: got %s", got)
	}

	if d, err := a.Walk(t, node, []byte(`"ok"`), nil); err != nil || len(d) != 0 {
		tb.Errorf("validate from ok: err=%v diag=%v", err, d)
	}

	if d, _ := a.Walk(t, node, []byte(`5`), nil); len(d) == 0 {
		tb.Errorf("validate from bad: want invalid")
	}

	var h Schema
	h.Resolve = func(base, ref string) ([]byte, error) {
		if ref == "urn:y" {
			return []byte(`{"$defs":{"Y":{"type":"integer"}}}`), nil
		}

		return nil, errors.New("unknown " + ref)
	}

	if err := h.Compile([]byte(`{}`)); err != nil {
		tb.Fatalf("compile: %v", err)
	}

	t, node, err = h.Lookup("urn:y#/$defs/Y")
	if err != nil {
		tb.Fatalf("lookup via hook: %v", err)
	}

	if t == &h || t.ID != "urn:y" {
		tb.Errorf("lookup via hook: doc ID %q, want %q", t.ID, "urn:y")
	}

	if got := string(t.FormatNode(nil, node)); got != `{"type":"integer"}` {
		tb.Errorf("lookup via hook: got %s", got)
	}

	myErr := errors.New("boom")

	var f Schema
	f.Resolve = func(base, ref string) ([]byte, error) { return nil, myErr }

	if err := f.Compile([]byte(`{}`)); err != nil {
		tb.Fatalf("compile: %v", err)
	}

	if _, _, err := f.Lookup("urn:z#/x"); !errors.Is(err, myErr) {
		tb.Errorf("lookup hook error: got %v, want %v", err, myErr)
	}
}

func TestRefPointer(tb *testing.T) {
	for _, tc := range []struct {
		schema, data string
		ok           bool
	}{
		{`{"$defs":{"U":{"items":{"type":"number"}}},"properties":{"a":{"$ref":"#/$defs/U/items"}}}`, `{"a":5}`, true},
		{`{"$defs":{"U":{"items":{"type":"number"}}},"properties":{"a":{"$ref":"#/$defs/U/items"}}}`, `{"a":"x"}`, false},
		{`{"properties":{"user":{"items":{"type":"number"}},"b":{"$ref":"#/properties/user/items"}}}`, `{"b":5}`, true},
		{`{"properties":{"user":{"items":{"type":"number"}},"b":{"$ref":"#/properties/user/items"}}}`, `{"b":"x"}`, false},
		{`{"$defs":{"L":{"allOf":[{"type":"integer"}]}},"properties":{"b":{"$ref":"#/$defs/L/allOf/0"}}}`, `{"b":5}`, true},
		{`{"$defs":{"L":{"allOf":[{"type":"integer"}]}},"properties":{"b":{"$ref":"#/$defs/L/allOf/0"}}}`, `{"b":"x"}`, false},
	} {
		s, err := Compile([]byte(tc.schema))
		if err != nil {
			tb.Errorf("compile %q: %v", tc.schema, err)
			continue
		}

		d, err := validate(s, []byte(tc.data))
		if err != nil {
			tb.Errorf("validate %q: %v", tc.data, err)
			continue
		}

		if (len(d) == 0) != tc.ok {
			tb.Errorf("validate %q against %q: ok=%v diag=%v", tc.data, tc.schema, tc.ok, d)
		}
	}

	for _, in := range []string{
		`{"properties":{"user":{"items":{"type":"number"}}},"$ref":"#/properties/user/type"}`,
		`{"enum":[1],"$ref":"#/enum/0"}`,
		`{"allOf":[{}],"$ref":"#/allOf/1"}`,
	} {
		var s Schema

		if err := s.Compile([]byte(in)); !errors.Is(err, ErrRef) {
			tb.Errorf("compile %q: err %v, want Is(ErrRef)", in, err)
		}
	}
}

func TestValidateFrom(tb *testing.T) {
	s, err := Compile([]byte(`{"properties":{"user":{"type":"object","items":{"type":"number"},"properties":{"n":{"minimum":3}}}},"required":["user"]}`))
	if err != nil {
		tb.Fatalf("compile: %v", err)
	}

	for _, tc := range []struct {
		ref, data string
		ok        bool
	}{
		{"#", `{"user":{"n":5}}`, true},
		{"#", `{}`, false},
		{"#/properties/user", `{"n":5}`, true},
		{"#/properties/user", `{"n":1}`, false},
		{"#/properties/user", `5`, false},
		{"#/properties/user/items", `5`, true},
		{"#/properties/user/items", `"x"`, false},
		{"#/properties/user/properties/n", `5`, true},
		{"#/properties/user/properties/n", `1`, false},
	} {
		var a Applier

		t, node, err := s.Lookup(tc.ref)
		if err != nil {
			tb.Errorf("lookup %q: %v", tc.ref, err)
			continue
		}

		d, err := a.Walk(t, node, []byte(tc.data), nil)
		if err != nil {
			tb.Errorf("validate %q from %q: %v", tc.data, tc.ref, err)
			continue
		}

		if (len(d) == 0) != tc.ok {
			tb.Errorf("validate %q from %q: ok=%v diag=%v", tc.data, tc.ref, tc.ok, d)
		}
	}
}
