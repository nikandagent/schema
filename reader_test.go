package schema

import (
	"math"
	"strconv"
	"testing"
)

func mustPanic(tb *testing.T, name string, f func()) {
	tb.Helper()

	defer func() {
		if recover() == nil {
			tb.Errorf("%s: expected panic", name)
		}
	}()

	f()
}

func TestBufferNodesLen(tb *testing.T) {
	s, err := Compile([]byte(`{"type":"string","x-type":"custom","properties":{"a":{},"b":{},"c":{}}}`))
	if err != nil {
		tb.Fatal(err)
	}

	b := s.Reader()

	ext := b.Keyword(s.Root(), Ext)
	if n := b.NodesLen(ext); n != 1 {
		tb.Errorf("ext len: got %d, want 1", n)
	}

	props := b.Keyword(s.Root(), Properties)
	if n := b.NodesLen(props); n != 3 {
		tb.Errorf("properties len: got %d, want 3", n)
	}
}

func TestBufferNodesAt(tb *testing.T) {
	s, err := Compile([]byte(`{"x-type":"custom","properties":{"a":{},"b":{}}}`))
	if err != nil {
		tb.Fatal(err)
	}

	b := s.Reader()

	ext := b.Keyword(s.Root(), Ext)

	k, v := b.NodesAt(ext, 0)
	if got := string(b.String(k)); got != "x-type" {
		tb.Errorf("ext key: got %q, want %q", got, "x-type")
	}
	if got := string(b.String(v)); got != "custom" {
		tb.Errorf("ext value: got %q, want %q", got, "custom")
	}

	if k, v := b.NodesAt(ext, 1); k.Op() != None || v.Op() != None {
		tb.Errorf("ext at 1: got %v/%v, want None/None", k, v)
	}
	if k, v := b.NodesAt(ext, -2); k.Op() != None || v.Op() != None {
		tb.Errorf("ext at -2: got %v/%v, want None/None", k, v)
	}

	props := b.Keyword(s.Root(), Properties)

	k, _ = b.NodesAt(props, -1)
	if got := string(b.Span(k)); got != "b" {
		tb.Errorf("properties at -1 key: got %q, want %q", got, "b")
	}
}

func TestBufferNodesPanic(tb *testing.T) {
	s, err := Compile([]byte(`{"type":"string","minimum":3}`))
	if err != nil {
		tb.Fatal(err)
	}

	b := s.Reader()

	typ := b.Keyword(s.Root(), Type)
	mustPanic(tb, "Nodes(Type)", func() { b.Nodes(typ) })
	mustPanic(tb, "NodesAt(Type)", func() { b.NodesAt(typ, 0) })

	num := b.Deref(b.Keyword(s.Root(), Minimum))
	mustPanic(tb, "Nodes(Number)", func() { b.Nodes(num) })
	mustPanic(tb, "NodesAt(Number)", func() { b.NodesAt(num, 0) })
}

func TestBufferExt(tb *testing.T) {
	s, err := Compile([]byte(`{"type":"string","x-type":"custom"}`))
	if err != nil {
		tb.Fatal(err)
	}

	b := s.Reader()

	if v := b.Ext(s.Root(), "x-type"); string(b.String(v)) != "custom" {
		tb.Errorf("ext x-type: got %q, want %q", b.String(v), "custom")
	}

	if v := b.Ext(s.Root(), "x-missing"); v.Op() != None {
		tb.Errorf("ext x-missing: got %v, want None", v)
	}

	typ := b.Keyword(s.Root(), Type)
	mustPanic(tb, "Ext(Type)", func() { b.Ext(typ, "x-type") })
}

func TestBufferRaw(tb *testing.T) {
	s, err := Compile([]byte(`{"title":"T","description":"D","properties":{"a":{"title":"A"}}}`))
	if err != nil {
		tb.Fatal(err)
	}

	b := s.Reader()

	if v := b.Raw(s.Root(), "title"); string(b.String(v)) != "T" {
		tb.Errorf("raw title: got %q, want %q", b.String(v), "T")
	}

	if v := b.Raw(s.Root(), "description"); string(b.String(v)) != "D" {
		tb.Errorf("raw description: got %q, want %q", b.String(v), "D")
	}

	if v := b.Raw(s.Root(), "format"); v.Op() != None {
		tb.Errorf("raw format: got %v, want None", v)
	}

	subs := 0

	for k, sub := range b.Iter(b.Keyword(s.Root(), Properties)) {
		subs++

		if got := string(b.String(b.Raw(sub, "title"))); got != "A" {
			tb.Errorf("raw title of %q: got %q, want %q", b.String(k), got, "A")
		}
	}

	if subs != 1 {
		tb.Errorf("properties count: got %d, want 1", subs)
	}

	n, err := Compile([]byte(`{"type":"string"}`))
	if err != nil {
		tb.Fatal(err)
	}

	if v := n.Reader().Raw(n.Root(), "title"); v.Op() != None {
		tb.Errorf("raw title with no annotations: got %v, want None", v)
	}

	x, err := Compile([]byte(`{"title":"T","x-type":"custom"}`))
	if err != nil {
		tb.Fatal(err)
	}

	xb := x.Reader()

	if v := xb.Raw(x.Root(), "x-type"); v.Op() != None {
		tb.Errorf("raw x-type: got %v, want None", v)
	}

	if v := xb.Ext(x.Root(), "title"); v.Op() != None {
		tb.Errorf("ext title: got %v, want None", v)
	}

	if v := xb.Ext(x.Root(), "x-type"); string(xb.String(v)) != "custom" {
		tb.Errorf("ext x-type: got %q, want %q", xb.String(v), "custom")
	}

	m, err := Compile([]byte(`{"title":5}`))
	if err != nil {
		tb.Fatal(err)
	}

	if v := m.Reader().Raw(m.Root(), "title"); v.Op() != Number {
		tb.Errorf("raw non-string title: got %v, want Number", v.Op())
	}

	e, err := Compile([]byte(`{"\u0074itle":"T"}`))
	if err != nil {
		tb.Fatal(err)
	}

	eb := e.Reader()

	if v := eb.Raw(e.Root(), "title"); string(eb.String(v)) != "T" {
		tb.Errorf("raw escaped title: got %q, want %q", eb.String(v), "T")
	}

	mustPanic(tb, "Raw(Properties)", func() { b.Raw(b.Keyword(s.Root(), Properties), "title") })
	mustPanic(tb, "Raw(String)", func() { b.Raw(b.Raw(s.Root(), "title"), "title") })
}

func TestBufferKeyword(tb *testing.T) {
	s, err := Compile([]byte(`{"type":"string"}`))
	if err != nil {
		tb.Fatal(err)
	}

	b := s.Reader()

	if op := b.Keyword(s.Root(), Type); op.Op() != Type {
		tb.Errorf("keyword Type: got %v", op.Op())
	}

	if op := b.Keyword(s.Root(), Minimum); op.Op() != None {
		tb.Errorf("keyword Minimum: got %v, want None", op)
	}

	typ := b.Keyword(s.Root(), Type)
	mustPanic(tb, "Keyword(Type)", func() { b.Keyword(typ, Type) })
}

func TestBufferFind(tb *testing.T) {
	s, err := Compile([]byte(`{"properties":{"a":{"type":"integer"},"b c":{"type":"string"},"":{"type":"null"},"café":{"type":"array"}},"patternProperties":{"^x":{"type":"boolean"}},"$defs":{"T":{"type":"number"},"a/b":{"type":"object"}}}`))
	if err != nil {
		tb.Fatal(err)
	}

	b := s.Reader()
	props := b.Keyword(s.Root(), Properties)

	for _, tc := range []struct {
		block Node
		key   string
		want  Types
	}{
		{props, "a", TypeInteger},
		{props, "b c", TypeString},
		{props, "", TypeNull},
		{props, "café", TypeArray},
		{b.Keyword(s.Root(), PatternProps), "^x", TypeBoolean},
		{b.Keyword(s.Root(), Defs), "T", TypeNumber},
		{b.Keyword(s.Root(), Defs), "a/b", TypeObject},
	} {
		sub := b.Find(tc.block, tc.key)
		if sub.Op() == None {
			tb.Errorf("find %q: None", tc.key)
			continue
		}

		if got := TypesOf(b.Keyword(sub, Type)); got != tc.want {
			tb.Errorf("find %q: type %v, want %v", tc.key, got, tc.want)
		}
	}

	for _, key := range []string{"z", "A", "b", "^y", " "} {
		if v := b.Find(props, key); v.Op() != None {
			tb.Errorf("find missing %q: got %v, want None", key, v)
		}
	}

	e, err := Compile([]byte(`{"properties":{"caf\u00e9":{"type":"integer"}}}`))
	if err != nil {
		tb.Fatal(err)
	}

	eb := e.Reader()

	if v := eb.Find(eb.Keyword(e.Root(), Properties), "café"); v.Op() == None {
		tb.Errorf("find escaped property name: None")
	}

	var buf Buffer

	buf.Reset()

	obj, err := buf.Writer().FromJSON([]byte(`{"a":1,"b c":2,"":3,"café":4,"esc\u0061":5}`))
	if err != nil {
		tb.Fatal(err)
	}

	r := buf.Reader()

	for _, tc := range []struct {
		key  string
		want string
	}{
		{"a", "1"},
		{"b c", "2"},
		{"", "3"},
		{"café", "4"},
		{"esca", "5"},
	} {
		v := r.Find(obj, tc.key)
		if v.Op() == None {
			tb.Errorf("find data %q: None", tc.key)
			continue
		}

		if got := string(r.AppendJSON(nil, v)); got != tc.want {
			tb.Errorf("find data %q: got %s, want %s", tc.key, got, tc.want)
		}
	}

	for _, key := range []string{"z", "A", "b", "caf", "esc"} {
		if v := r.Find(obj, key); v.Op() != None {
			tb.Errorf("find missing data %q: got %v, want None", key, v)
		}
	}

	esc, err := buf.Writer().FromJSON([]byte(`{"caf\u00e9":1}`))
	if err != nil {
		tb.Fatal(err)
	}

	if v := r.Find(esc, "café"); v.Op() == None {
		tb.Errorf("find escaped data key: None")
	}

	dup, err := buf.Writer().FromJSON([]byte(`{"a":1,"a":2}`))
	if err != nil {
		tb.Fatal(err)
	}

	// duplicate keys: first match wins
	if got := string(r.AppendJSON(nil, r.Find(dup, "a"))); got != "1" {
		tb.Errorf("find duplicate key: got %s, want 1", got)
	}

	mustPanic(tb, "Find(String)", func() { r.Find(r.Find(obj, "a"), "a") })
	mustPanic(tb, "Find(All)", func() { b.Find(s.Root(), "a") })
	mustPanic(tb, "Find(Type)", func() { b.Find(b.Keyword(s.Root(), Type), "a") })

	x, err := Compile([]byte(`{"title":"t","x-type":"custom"}`))
	if err != nil {
		tb.Fatal(err)
	}

	xb := x.Reader()

	for _, kw := range []Opcode{Raw, Ext} {
		op := xb.Keyword(x.Root(), kw)
		mustPanic(tb, "Find(pair keyword)", func() { xb.Find(op, "title") })
	}
}

func TestBufferIter(tb *testing.T) {
	s, err := Compile([]byte(`{"type":"string","properties":{"a":{},"b":{}},"allOf":[{},{},{}],"not":{},"additionalProperties":{"type":"integer"}}`))
	if err != nil {
		tb.Fatal(err)
	}

	b := s.Reader()

	var names []string
	for k, v := range b.Iter(b.Keyword(s.Root(), Properties)) {
		names = append(names, string(b.String(k)))
		if v.Op() != All {
			tb.Errorf("properties sub: got %v, want All", v.Op())
		}
	}
	if len(names) != 2 || names[0] != "a" || names[1] != "b" {
		tb.Errorf("properties names: got %v", names)
	}

	n := 0
	for k := range b.Iter(b.Keyword(s.Root(), AllOf)) {
		if k.Int() != int64(n) {
			tb.Errorf("allOf index: got %d, want %d", k.Int(), n)
		}
		n++
	}
	if n != 3 {
		tb.Errorf("allOf count: got %d, want 3", n)
	}

	n = 0
	for k := range b.Iter(b.Keyword(s.Root(), Not)) {
		n++
		if k.Op() != None {
			tb.Errorf("not key: got %v, want None", k)
		}
	}
	if n != 1 {
		tb.Errorf("not count: got %d, want 1", n)
	}

	n = 0
	for k, v := range b.Iter(b.Keyword(s.Root(), Additional)) {
		n++
		if k.Op() != None || v.Op() != All {
			tb.Errorf("additional: got %v/%v, want None/All", k, v.Op())
		}
	}
	if n != 1 {
		tb.Errorf("additional count: got %d, want 1", n)
	}

	for range b.Iter(b.Keyword(s.Root(), Type)) {
		tb.Errorf("type: yielded a pair, want none")
	}

	c, err := Compile([]byte(`{"if":{"type":"string"},"then":{"minLength":2},"else":{"type":"integer"}}`))
	if err != nil {
		tb.Fatal(err)
	}

	cb := c.Reader()

	for _, kw := range []Opcode{If, Then, Else} {
		n = 0

		for k, v := range cb.Iter(cb.Keyword(c.Root(), kw)) {
			n++

			if k.Op() != None || v.Op() != All {
				tb.Errorf("%v: got %v/%v, want None/All", kw, k, v.Op())
			}
		}

		if n != 1 {
			tb.Errorf("%v count: got %d, want 1", kw, n)
		}
	}

	if got := string(c.FormatNode(nil, cb.Deref(cb.Keyword(c.Root(), If)))); got != `{"type":"string"}` {
		tb.Errorf("if condition: got %q", got)
	}

	p, err := Compile([]byte(`{"prefixItems":[{"type":"integer"},{"type":"string"}],"items":{"type":"number"}}`))
	if err != nil {
		tb.Fatal(err)
	}

	pb := p.Reader()

	n = 0
	for k, v := range pb.Iter(pb.Keyword(p.Root(), Prefix)) {
		if k.Int() != int64(n) || v.Op() != All {
			tb.Errorf("prefixItems %d: got %d/%v, want %d/All", n, k.Int(), v.Op(), n)
		}
		n++
	}
	if n != 2 {
		tb.Errorf("prefixItems count: got %d, want 2", n)
	}

	if got := string(p.FormatNode(nil, pb.Deref(pb.Keyword(p.Root(), Items)))); got != `{"type":"number"}` {
		tb.Errorf("linked items sub: got %q", got)
	}

	n = 0
	for range b.Iter(b.Keyword(s.Root(), Properties)) {
		n++
		break
	}
	if n != 1 {
		tb.Errorf("break: got %d iterations, want 1", n)
	}
}

func TestBufferString(tb *testing.T) {
	for _, tc := range []struct {
		in   string
		kw   Opcode
		want string
	}{
		{`{"pattern":"^a"}`, Pattern, `^a`},
		{`{"pattern":"\\d+"}`, Pattern, `\d+`},
		{`{"pattern":"\u0061+"}`, Pattern, `a+`},

		{`{"$defs":{"T":{}},"$ref":"#/$defs/T"}`, Ref, `#/$defs/T`},
		{`{"$defs":{"a":{}},"$ref":"#/$defs/\u0061"}`, Ref, `#/$defs/a`},
		{`{"$defs":{"a\\b":{}},"$ref":"#/$defs/a\\b"}`, Ref, `#/$defs/a\b`},
	} {
		s, err := Compile([]byte(tc.in))
		if err != nil {
			tb.Errorf("compile %q: %v", tc.in, err)
			continue
		}

		b := s.Reader()

		op := b.Keyword(s.Root(), tc.kw)
		if op.Op() == None {
			tb.Errorf("string %q: no %v keyword", tc.in, tc.kw)
			continue
		}

		if got := string(b.String(op)); got != tc.want {
			tb.Errorf("string %q: got %q, want %q", tc.in, got, tc.want)
		}
	}

	var buf Buffer

	buf.Reset()

	r, w := buf.Reader(), buf.Writer()

	if got := string(r.String(w.String(`a"b`))); got != `a"b` {
		tb.Errorf("string String: got %q, want %q", got, `a"b`)
	}

	// a written String holds the bytes it was given, verbatim: String and Span
	// are the same view of it, with nothing left to decode
	for _, in := range []string{`key`, `a\u0062`, `a\z`, ``} {
		op := w.Span(String, []byte(in))

		if got := string(r.String(op)); got != in {
			tb.Errorf("string String %q: got %q", in, got)
		}
		if got := string(r.Span(op)); got != in {
			tb.Errorf("span String %q: got %q", in, got)
		}
	}

	mustPanic(tb, "String(Number)", func() { r.String(w.Span(Number, []byte(`5`))) })
	mustPanic(tb, "String(IntLit)", func() { r.String(w.Int(5)) })
	mustPanic(tb, "String(Null)", func() { r.String(w.Null()) })
}

func TestAppendPointer(tb *testing.T) {
	var b Buffer

	b.Reset()

	r, w := b.Reader(), b.Writer()

	key := func(s string) Step { return Step{DataKey: w.Span(String, []byte(s))} }
	idx := func(i int) Step { return Step{DataKey: MakeInt(int64(i))} }
	skip := Step{}

	for _, tc := range []struct {
		steps []Step
		want  string
	}{
		{nil, "."},
		{[]Step{key("a")}, ".a"},
		{[]Step{key("users"), idx(0), key("name")}, ".users[0].name"},
		{[]Step{idx(2), idx(10)}, "[2][10]"},
		{[]Step{key("_x9")}, "._x9"},
		{[]Step{key("a b")}, `."a b"`},
		{[]Step{key("a-b")}, `."a-b"`},
		{[]Step{key("1x")}, `."1x"`},
		{[]Step{key("")}, `.""`},
		{[]Step{key(`q"z`)}, `."q\"z"`},
		{[]Step{key("café")}, `."café"`},
		{[]Step{skip}, "."},
		{[]Step{skip, key("a"), skip, idx(1), skip}, ".a[1]"},
	} {
		if got := string(r.AppendPointer(nil, tc.steps)); got != tc.want {
			tb.Errorf("pointer %d steps: got %s, want %s", len(tc.steps), got, tc.want)
		}
	}

	if got := string(r.AppendPointer([]byte("path: "), []Step{key("a")})); got != "path: .a" {
		tb.Errorf("append to buffer: got %q", got)
	}
}

func TestBufferDeref(tb *testing.T) {
	for _, tc := range []struct {
		schema string
		want   Opcode
		kind   Opcode
		span   string
	}{
		{`{"not":{"type":"string"}}`, Not, All, ""},
		{`{"items":{"type":"number"}}`, Items, All, ""},
		{`{"const":5}`, Const, Number, "5"},
		{`{"minimum":3}`, Minimum, Number, "3"},
		{`{"if":{"type":"string"},"then":{"minLength":2},"else":{"type":"integer"}}`, If, All, ""},
		{`{"if":{"type":"string"},"then":{"minLength":2},"else":{"type":"integer"}}`, Then, All, ""},
		{`{"if":{"type":"string"},"then":{"minLength":2},"else":{"type":"integer"}}`, Else, All, ""},
		{`{"if":{"type":"string"}}`, If, All, ""},
		{`{"prefixItems":[{"type":"integer"}],"items":{"type":"number"}}`, Items, All, ""},
		{`{"prefixItems":[{"type":"integer"}],"items":false}`, Items, Fail, ""},
	} {
		s, err := Compile([]byte(tc.schema))
		if err != nil {
			tb.Errorf("compile %q: %v", tc.schema, err)
			continue
		}

		b := s.Reader()

		ch := b.Deref(b.Keyword(s.Root(), tc.want))
		if ch.Op() != tc.kind {
			tb.Errorf("deref %q: got kind %v, want %v", tc.schema, ch.Op(), tc.kind)
		}
		if tc.span != "" && string(b.Span(ch)) != tc.span {
			tb.Errorf("deref %q: got span %q, want %q", tc.schema, b.Span(ch), tc.span)
		}
	}

	s, err := Compile([]byte(`{"properties":{"a":{}},"type":"string"}`))
	if err != nil {
		tb.Fatal(err)
	}

	b := s.Reader()

	mustPanic(tb, "Deref(Properties)", func() { b.Deref(b.Keyword(s.Root(), Properties)) })
	mustPanic(tb, "Deref(Type)", func() { b.Deref(b.Keyword(s.Root(), Type)) })
}

// TestNodeSrc pins that every node in a compiled program knows the schema text
// it came from: a keyword node spans its whole "key": value pair, a schema
// object its braces.
func TestNodeSrc(tb *testing.T) {
	src := `{"$ref":"#/$defs/T","$defs":{"T":{"type":"string","pattern":"^a\u0062c$"}},"minLength":3}`

	s, err := Compile([]byte(src))
	if err != nil {
		tb.Fatal(err)
	}

	r := s.Reader()
	defs := r.Keyword(s.Root(), Defs)
	sub := r.Find(defs, "T")
	key, _ := r.NodesAt(defs, 0)

	for _, tc := range []struct {
		name string
		op   Node
		want string
	}{
		{"root", s.Root(), src},
		{"$ref", r.Keyword(s.Root(), Ref), `"$ref":"#/$defs/T"`},
		{"minLength", r.Keyword(s.Root(), MinLen), `"minLength":3`},
		{"$defs", defs, `"$defs":{"T":{"type":"string","pattern":"^a\u0062c$"}}`},
		{"defs key", key, `"T"`},
		{"defs subschema", sub, `{"type":"string","pattern":"^a\u0062c$"}`},
		{"type", r.Keyword(sub, Type), `"type":"string"`},
		// an escaped pattern keeps its place: the span is the token as written
		{"pattern", r.Keyword(sub, Pattern), `"pattern":"^a\u0062c$"`},
	} {
		off, end, ok := tc.op.Src()
		if !ok || src[off:end] != tc.want {
			tb.Errorf("%s: src %d:%d ok=%v %q, want %q", tc.name, off, end, ok, src[off:end], tc.want)
		}
	}

	if got := string(r.String(r.Keyword(sub, Pattern))); got != "^abc$" {
		tb.Errorf("pattern value: got %q, want %q", got, "^abc$")
	}

	var b Buffer

	b.Reset()

	w := b.Writer()

	for _, op := range []Node{w.String("x"), w.Int(5), w.Float(1.5), w.Bool(true), w.Null(), {}} {
		if off, end, ok := op.Src(); ok {
			tb.Errorf("src of synthesized %v: %d:%d ok=true, want none", op.Op(), off, end)
		}
	}
}

// TestDiagInSchema locates a validation finding in the schema text, not only in
// the document: the keyword node the Diag carries knows where it was written.
func TestDiagInSchema(tb *testing.T) {
	src := `{"properties":{"n":{"minLength":3},"s":{"type":"integer"}}}`

	s, err := Compile([]byte(src))
	if err != nil {
		tb.Fatal(err)
	}

	d, err := validate(s, []byte(`{"n":"ab","s":"x"}`))
	if err != nil || len(d) != 2 {
		tb.Fatalf("diags=%v err=%v, want 2", d, err)
	}

	want := map[DiagCode]string{
		TooShort:     `"minLength":3`,
		TypeMismatch: `"type":"integer"`,
	}

	for _, x := range d {
		off, end, ok := x.Op.Src()
		if !ok {
			tb.Errorf("%v: the keyword has no place in the schema", x.Code)
			continue
		}

		if got := src[off:end]; got != want[x.Code] {
			tb.Errorf("%v: schema span %q, want %q", x.Code, got, want[x.Code])
		}
	}
}

// TestLiteralExact pins that a literal node carries the number the caller wrote,
// to the last bit — the packed-into-the-opcode form used to round it.
func TestLiteralExact(tb *testing.T) {
	for _, v := range []int64{0, 1, -1, 1 << 30, 1 << 62, -1 << 62, 1<<63 - 1, -1 << 63, 1 << 56} {
		n := MakeInt(v)

		if n.Op() != IntLit || n.Int() != v {
			tb.Errorf("MakeInt(%d): op=%v Int=%d", v, n.Op(), n.Int())
		}
	}

	for _, v := range []float64{0, 0.1, -0.1, 1.5, 1e300, 1e-300, 1.0 / 3, math.Pi, math.MaxFloat64, math.SmallestNonzeroFloat64} {
		n := MakeFlt(v)

		if n.Op() != FltLit || n.Flt() != v {
			tb.Errorf("MakeFlt(%v): op=%v Flt=%v", v, n.Op(), n.Flt())
		}
	}

	if n := MakeFlt(math.NaN()); !math.IsNaN(n.Flt()) {
		tb.Errorf("MakeFlt(NaN): %v", n.Flt())
	}

	// the spellings the 56-bit packing used to lose
	for _, v := range []float64{0.1, 1.0 / 3, math.Pi} {
		if got := strconv.FormatFloat(MakeFlt(v).Flt(), 'g', -1, 64); got != strconv.FormatFloat(v, 'g', -1, 64) {
			tb.Errorf("MakeFlt(%v) formats as %s", v, got)
		}
	}

	// a literal spends meta on its value, so it has no source position
	if _, _, ok := MakeInt(5).Src(); ok {
		tb.Errorf("MakeInt(5).Src(): ok=true")
	}
	if _, _, ok := MakeFlt(1.5).Src(); ok {
		tb.Errorf("MakeFlt(1.5).Src(): ok=true")
	}
}

// TestNodeZero pins the zero Node as the absent one: None, no source, no value,
// and what every lookup returns when it finds nothing.
func TestNodeZero(tb *testing.T) {
	var zero Node

	if zero.Op() != None {
		tb.Errorf("Node{}.Op() = %v, want None", zero.Op())
	}

	if _, _, ok := zero.Src(); ok {
		tb.Errorf("Node{}.Src(): ok=true")
	}

	if zero.Keyword() != "" {
		tb.Errorf("Node{}.Keyword() = %q", zero.Keyword())
	}

	if TypesOf(zero) != 0 {
		tb.Errorf("TypesOf(Node{}) = %v, want the empty set", TypesOf(zero))
	}

	s, err := Compile([]byte(`{"type":"string","properties":{"a":{}}}`))
	if err != nil {
		tb.Fatal(err)
	}

	r := s.Reader()

	for _, tc := range []struct {
		name string
		op   Node
	}{
		{"Keyword(missing)", r.Keyword(s.Root(), Minimum)},
		{"Raw(missing)", r.Raw(s.Root(), "title")},
		{"Ext(missing)", r.Ext(s.Root(), "x-none")},
		{"Find(missing)", r.Find(r.Keyword(s.Root(), Properties), "zz")},
	} {
		if tc.op != zero {
			tb.Errorf("%s: got %+v, want the zero Node", tc.name, tc.op)
		}
	}
}

// TestBufferParts unfolds the three keyword families the compiler links into one
// node: each splitter takes the folded node and hands back the siblings, Pass
// where a sibling was not written.
func TestBufferParts(tb *testing.T) {
	s, err := Compile([]byte(`{
		"properties":{"a":{"type":"integer"}},
		"patternProperties":{"^x":{"type":"string"}},
		"additionalProperties":false,
		"prefixItems":[{"type":"integer"}],
		"items":{"type":"string"},
		"if":{"type":"object"},"then":{"minProperties":1},"else":{"maxLength":3}
	}`))
	if err != nil {
		tb.Fatal(err)
	}

	r := s.Reader()

	props, patterns, sub := r.PropertiesParts(r.Keyword(s.Root(), Additional))

	for _, tc := range []struct {
		name string
		op   Node
		want string
	}{
		{"properties", props, `{"a":{"type":"integer"}}`},
		{"patternProperties", patterns, `{"^x":{"type":"string"}}`},
		{"additional sub", sub, `false`},
	} {
		if got := string(s.FormatKeyword(nil, tc.op)); got != tc.want {
			tb.Errorf("PropertiesParts %s: got %s, want %s", tc.name, got, tc.want)
		}
	}

	if props.Op() != Properties || patterns.Op() != PatternProps || sub.Op() != Fail {
		tb.Errorf("PropertiesParts kinds: %v/%v/%v", props.Op(), patterns.Op(), sub.Op())
	}

	prefix, tail := r.ItemsParts(r.Keyword(s.Root(), Items))

	if got := string(s.FormatKeyword(nil, prefix)); got != `[{"type":"integer"}]` {
		tb.Errorf("ItemsParts prefix: got %s", got)
	}
	if got := string(s.FormatNode(nil, tail)); got != `{"type":"string"}` {
		tb.Errorf("ItemsParts sub: got %s", got)
	}

	cond, then, els := r.CondParts(r.Keyword(s.Root(), If))

	for _, tc := range []struct {
		name string
		op   Node
		want string
	}{
		{"if", cond, `{"type":"object"}`},
		{"then", then, `{"minProperties":1}`},
		{"else", els, `{"maxLength":3}`},
	} {
		if got := string(s.FormatNode(nil, tc.op)); got != tc.want {
			tb.Errorf("CondParts %s: got %s, want %s", tc.name, got, tc.want)
		}
	}

	// a sibling that was not written comes back as Pass
	lone, err := Compile([]byte(`{"additionalProperties":{"type":"integer"},"items":{"type":"string"},"if":{"type":"object"}}`))
	if err != nil {
		tb.Fatal(err)
	}

	lr := lone.Reader()

	props, patterns, sub = lr.PropertiesParts(lr.Keyword(lone.Root(), Additional))
	if props.Op() != Pass || patterns.Op() != Pass || sub.Op() != All {
		tb.Errorf("lone PropertiesParts: %v/%v/%v, want Pass/Pass/All", props.Op(), patterns.Op(), sub.Op())
	}

	prefix, tail = lr.ItemsParts(lr.Keyword(lone.Root(), Items))
	if prefix.Op() != Pass || tail.Op() != All {
		tb.Errorf("lone ItemsParts: %v/%v, want Pass/All", prefix.Op(), tail.Op())
	}

	cond, then, els = lr.CondParts(lr.Keyword(lone.Root(), If))
	if cond.Op() != All || then.Op() != Pass || els.Op() != Pass {
		tb.Errorf("lone CondParts: %v/%v/%v, want All/Pass/Pass", cond.Op(), then.Op(), els.Op())
	}
}

// TestSourceIntern pins that a value keeps its place in the input whether the
// arena borrowed the bytes or interned a copy of them — the two paths a document
// reaches a Buffer by.
func TestSourceIntern(tb *testing.T) {
	src := []byte(`{"n":12345,"f":-1.5e3,"s":"ab","t":true,"f2":false,"z":null}`)

	var borrow, intern Buffer

	borrow.Reset()
	intern.Reset()

	brt, err := borrow.decode(src)
	if err != nil {
		tb.Fatalf("decode: %v", err)
	}

	irt, err := intern.Writer().FromJSON(src)
	if err != nil {
		tb.Fatalf("fromjson: %v", err)
	}

	bn, in := borrow.Reader().Nodes(brt), intern.Reader().Nodes(irt)

	if len(bn) != len(in) {
		tb.Fatalf("nodes: %d vs %d", len(bn), len(in))
	}

	for i := range bn {
		boff, bend, bok := bn[i].Src()
		ioff, iend, iok := in[i].Src()

		if !bok || !iok || boff != ioff || bend != iend {
			tb.Errorf("node %d (%v): borrowed %d:%d ok=%v, interned %d:%d ok=%v",
				i, bn[i].Op(), boff, bend, bok, ioff, iend, iok)
			continue
		}

		if bn[i].Op() != in[i].Op() {
			tb.Errorf("node %d: kinds %v vs %v", i, bn[i].Op(), in[i].Op())
		}
	}

	// and the spans really are the tokens
	for i, want := range []string{`"n"`, `12345`, `"f"`, `-1.5e3`, `"s"`, `"ab"`, `"t"`, `true`, `"f2"`, `false`, `"z"`, `null`} {
		off, end, ok := in[i].Src()
		if !ok || string(src[off:end]) != want {
			tb.Errorf("interned node %d: %d:%d ok=%v %q, want %q", i, off, end, ok, src[off:end], want)
		}
	}
}
