package schema

import (
	"errors"

	"nikand.dev/go/json2"
)

type (
	def struct {
		name string // full pointer, e.g. "#/$defs/Name"
		root Opcode
	}

	hook struct {
		name string // keyword suffix after "x-", e.g. "type"
		h    Handler
	}
)

var ErrSchema = errors.New("bad schema")

const (
	typeNull = 1 << iota
	typeBool
	typeInt
	typeNum
	typeStr
	typeArr
	typeObj
)

// Compile parses a schema document into a program.
func Compile(b []byte) (*Schema, error) {
	var s Schema

	err := s.Compile(b)
	if err != nil {
		return nil, err
	}

	return &s, nil
}

func (s *Schema) Compile(schema []byte) error {
	b := &s.prog

	b.ro = false // writable while compiling, read-only after
	b.src = schema
	b.code = b.code[:0]
	b.text = b.text[:0]
	b.tmp = b.tmp[:0]

	s.defs = s.defs[:0]

	if b.text == nil {
		b.text = b.textbuf[:]
	}

	var d json2.Iterator

	root, i, err := s.compile(schema, 0)
	if err != nil {
		return err
	}

	i = d.SkipSpaces(schema, i)
	if i != len(schema) {
		return ErrSchema
	}

	s.root = root
	s.prog.ro = true

	return s.checkRefs()
}

// SetXHook binds h to the custom keyword "x-"+name. When Compile meets that
// keyword it compiles to a CallExt dispatching to h during Walk/Rewrite, instead
// of keeping it as an inert Raw annotation. Register before Compile.
func (s *Schema) SetXHook(name string, h Handler) {
	for i := range s.xhooks {
		if s.xhooks[i].name == name {
			s.xhooks[i].h = h
			return
		}
	}

	s.xhooks = append(s.xhooks, hook{name: name, h: h})
}

func (s *Schema) compile(b []byte, st int) (Opcode, int, error) {
	var d json2.Iterator

	tp, i, err := d.Type(b, st)
	if err != nil {
		return 0, i, err
	}

	switch tp {
	case json2.Object:
		return s.object(b, i)
	case json2.Bool:
		op := Fail
		if b[i] == 't' {
			op = Pass
		}

		i, err = d.Skip(b, i)
		return op, i, err
	default:
		return 0, i, ErrSchema
	}
}

func (s *Schema) object(b []byte, st int) (Opcode, int, error) {
	mark := len(s.prog.tmp)
	defer func() { s.prog.tmp = s.prog.tmp[:mark] }()

	var d json2.Iterator

	i, err := d.Enter(b, st, json2.Object)
	if err != nil {
		return 0, i, err
	}

	var key []byte
	var op Opcode

	for d.ForMore(b, &i, json2.Object, &err) {
		kst := i

		key, i, err = d.Key(b, i)
		if err != nil {
			return 0, i, err
		}

		op, i, err = s.keyword(key, b, kst, i)
		if err != nil {
			return 0, i, err
		}

		if op != Pass {
			s.prog.tmp = append(s.prog.tmp, op)
		}
	}
	if err != nil {
		return 0, i, err
	}

	if !s.Flags.Is(SchemaKeepOrder) {
		s.canonRequired(s.prog.tmp[mark:])
		sortKeywords(s.prog.tmp[mark:])
	}

	n := len(s.prog.tmp) - mark
	off := len(s.prog.code)
	s.prog.code = append(s.prog.code, s.prog.tmp[mark:]...)

	return makeNode(And, off, n), i, nil
}

func (s *Schema) keyword(name, b []byte, kst, st int) (Opcode, int, error) {
	switch string(name) {
	case "type":
		return s.kwType(b, st)
	case "properties":
		return s.kwProps(b, st)
	case "required":
		return s.kwList(Required, b, st)
	case "enum":
		return s.kwList(Enum, b, st)
	case "const":
		return s.kwValue(Const, b, st)
	case "default":
		return s.kwValue(Default, b, st)
	case "minimum":
		return s.kwValue(Minimum, b, st)
	case "maximum":
		return s.kwValue(Maximum, b, st)
	case "exclusiveMinimum":
		return s.kwValue(ExclMin, b, st)
	case "exclusiveMaximum":
		return s.kwValue(ExclMax, b, st)
	case "multipleOf":
		return s.kwValue(MultipleOf, b, st)
	case "items":
		return s.kwSub(Items, b, st)
	case "additionalProperties":
		return s.kwSub(Additional, b, st)
	case "not":
		return s.kwSub(Not, b, st)
	case "allOf":
		return s.kwSchemas(AllOf, b, st)
	case "anyOf":
		return s.kwSchemas(AnyOf, b, st)
	case "oneOf":
		return s.kwSchemas(OneOf, b, st)
	case "minLength":
		return s.kwImm(MinLen, b, st)
	case "maxLength":
		return s.kwImm(MaxLen, b, st)
	case "minItems":
		return s.kwImm(MinItems, b, st)
	case "maxItems":
		return s.kwImm(MaxItems, b, st)
	case "minProperties":
		return s.kwImm(MinProps, b, st)
	case "maxProperties":
		return s.kwImm(MaxProps, b, st)
	case "uniqueItems":
		return s.kwUnique(b, st)
	case "pattern":
		return s.kwPattern(b, st)
	case "$ref":
		return s.kwRef(b, st)
	case "$defs", "definitions":
		return s.kwDefs(name, b, st)
	default:
		if i, ok := s.hookXIndex(name); ok {
			return s.kwHook(i, b, st)
		}

		return s.kwUnknown(name, b, kst, st)
	}
}

func (s *Schema) kwType(b []byte, st int) (Opcode, int, error) {
	var d json2.Iterator

	tp, i, err := d.Type(b, st)
	if err != nil {
		return 0, i, err
	}

	var mask int
	var name []byte

	switch tp {
	case json2.String:
		name, i, err = d.Key(b, i)
		if err != nil {
			return 0, i, err
		}

		mask = typeBit(name)
	case json2.Array:
		i, err = d.Enter(b, i, json2.Array)
		if err != nil {
			return 0, i, err
		}

		for d.ForMore(b, &i, json2.Array, &err) {
			name, i, err = d.Key(b, i)
			if err != nil {
				return 0, i, err
			}

			mask |= typeBit(name)
		}
		if err != nil {
			return 0, i, err
		}
	default:
		return 0, i, ErrSchema
	}

	return makeImm(Type, mask), i, nil
}

func (s *Schema) kwProps(b []byte, st int) (Opcode, int, error) {
	mark := len(s.prog.tmp)
	defer func() { s.prog.tmp = s.prog.tmp[:mark] }()

	var d json2.Iterator

	i, err := d.Enter(b, st, json2.Object)
	if err != nil {
		return 0, i, err
	}

	var key, sub Opcode

	for d.ForMore(b, &i, json2.Object, &err) {
		key, i, err = s.literal(b, i)
		if err != nil {
			return 0, i, err
		}

		sub, i, err = s.compile(b, i)
		if err != nil {
			return 0, i, err
		}

		s.prog.tmp = append(s.prog.tmp, key, sub)
	}
	if err != nil {
		return 0, i, err
	}

	n := (len(s.prog.tmp) - mark) / 2
	off := len(s.prog.code)
	s.prog.code = append(s.prog.code, s.prog.tmp[mark:]...)

	return makeNode(Properties, off, n), i, nil
}

func (s *Schema) kwList(op Opcode, b []byte, st int) (Opcode, int, error) {
	mark := len(s.prog.tmp)
	defer func() { s.prog.tmp = s.prog.tmp[:mark] }()

	var d json2.Iterator

	i, err := d.Enter(b, st, json2.Array)
	if err != nil {
		return 0, i, err
	}

	var val Opcode

	for d.ForMore(b, &i, json2.Array, &err) {
		val, i, err = s.literal(b, i)
		if err != nil {
			return 0, i, err
		}

		s.prog.tmp = append(s.prog.tmp, val)
	}
	if err != nil {
		return 0, i, err
	}

	n := len(s.prog.tmp) - mark
	off := len(s.prog.code)
	s.prog.code = append(s.prog.code, s.prog.tmp[mark:]...)

	return makeNode(op, off, n), i, nil
}

func (s *Schema) kwSchemas(op Opcode, b []byte, st int) (Opcode, int, error) {
	mark := len(s.prog.tmp)
	defer func() { s.prog.tmp = s.prog.tmp[:mark] }()

	var d json2.Iterator

	i, err := d.Enter(b, st, json2.Array)
	if err != nil {
		return 0, i, err
	}

	var sub Opcode

	for d.ForMore(b, &i, json2.Array, &err) {
		sub, i, err = s.compile(b, i)
		if err != nil {
			return 0, i, err
		}

		s.prog.tmp = append(s.prog.tmp, sub)
	}
	if err != nil {
		return 0, i, err
	}

	n := len(s.prog.tmp) - mark
	off := len(s.prog.code)
	s.prog.code = append(s.prog.code, s.prog.tmp[mark:]...)

	return makeNode(op, off, n), i, nil
}

func (s *Schema) kwSub(op Opcode, b []byte, st int) (Opcode, int, error) {
	sub, i, err := s.compile(b, st)
	if err != nil {
		return 0, i, err
	}

	off := len(s.prog.code)
	s.prog.code = append(s.prog.code, sub)

	return makeNode(op, off, 1), i, nil
}

func (s *Schema) kwValue(op Opcode, b []byte, st int) (Opcode, int, error) {
	val, i, err := s.literal(b, st)
	if err != nil {
		return 0, i, err
	}

	off := len(s.prog.code)
	s.prog.code = append(s.prog.code, val)

	return makeNode(op, off, 1), i, nil
}

func (s *Schema) kwImm(op Opcode, b []byte, st int) (Opcode, int, error) {
	var d json2.Iterator

	raw, i, err := d.Raw(b, st)
	if err != nil {
		return 0, i, err
	}

	n, err := json2.Value(raw).Int()
	if err != nil {
		return 0, i, ErrSchema
	}

	return makeImm(op, n), i, nil
}

func (s *Schema) kwUnique(b []byte, st int) (Opcode, int, error) {
	var d json2.Iterator

	raw, i, err := d.Raw(b, st)
	if err != nil {
		return 0, i, err
	}

	v, err := json2.Value(raw).Bool()
	if err != nil {
		return 0, i, ErrSchema
	}

	if !v {
		return Pass, i, nil
	}

	return Unique, i, nil
}

func (s *Schema) kwPattern(b []byte, st int) (Opcode, int, error) {
	var d json2.Iterator

	tp, i, err := d.Type(b, st)
	if err != nil {
		return 0, i, err
	}

	if tp != json2.String {
		return 0, i, ErrSchema
	}

	j, err := d.Skip(b, i)
	if err != nil {
		return 0, j, err
	}

	return makeNode(Pattern, i, j-i), j, nil
}

func (s *Schema) kwRef(b []byte, st int) (Opcode, int, error) {
	var d json2.Iterator

	tp, i, err := d.Type(b, st)
	if err != nil {
		return 0, i, err
	}

	if tp != json2.String {
		return 0, i, ErrSchema
	}

	j, err := d.Skip(b, i)
	if err != nil {
		return 0, j, err
	}

	// pragmatic: only internal pointers, no external refs or ~0/~1 unescaping
	// (the latest spec resolves full URI references).
	off, n := i+1, j-i-2 // strip the quotes
	if n < 1 || b[off] != '#' {
		return 0, i, ErrSchema
	}

	return makeNode(Ref, off, n), j, nil
}

func (s *Schema) kwDefs(name, b []byte, st int) (Opcode, int, error) {
	var d json2.Iterator

	i, err := d.Enter(b, st, json2.Object)
	if err != nil {
		return 0, i, err
	}

	prefix := "#/" + string(name) + "/"

	var key []byte
	var sub Opcode

	for d.ForMore(b, &i, json2.Object, &err) {
		key, i, err = d.Key(b, i)
		if err != nil {
			return 0, i, err
		}

		sub, i, err = s.compile(b, i)
		if err != nil {
			return 0, i, err
		}

		s.defs = append(s.defs, def{prefix + string(key), sub})
	}
	if err != nil {
		return 0, i, err
	}

	return Pass, i, nil
}

func (s *Schema) kwUnknown(name, b []byte, kst, st int) (Opcode, int, error) {
	if !knownKeyword(name) && s.Flags.Is(SchemaRejectUnknown) {
		return 0, st, ErrSchema
	}

	var d json2.Iterator

	kend, err := d.Skip(b, kst)
	if err != nil {
		return 0, kend, err
	}

	key := makeNode(Str, kst, kend-kst)

	val, i, err := s.literal(b, st)
	if err != nil {
		return 0, i, err
	}

	off := len(s.prog.code)
	s.prog.code = append(s.prog.code, key, val)

	return makeNode(Raw, off, 2), i, nil
}

// kwHook compiles a hooked keyword to a CallExt: off → its value operand in the
// program arena, arg → the hook index dispatched to at apply time.
func (s *Schema) kwHook(i int, b []byte, st int) (Opcode, int, error) {
	val, j, err := s.literal(b, st)
	if err != nil {
		return 0, j, err
	}

	off := len(s.prog.code)
	s.prog.code = append(s.prog.code, val)

	return makeNode(CallExt, off, i), j, nil
}

func (s *Schema) hookXIndex(name []byte) (int, bool) {
	if len(name) < 3 || name[0] != 'x' || name[1] != '-' {
		return 0, false
	}

	name = name[2:]

	for i := range s.xhooks {
		if s.xhooks[i].name == string(name) {
			return i, true
		}
	}

	return 0, false
}

func (s *Schema) checkRefs() error {
	for _, op := range s.prog.code {
		if op.Op() == Ref && s.refTarget(op) == bad {
			return ErrSchema
		}
	}

	return nil
}

func (s *Schema) refTarget(op Opcode) Opcode {
	name := s.prog.Span(op)

	if string(name) == "#" {
		return s.root
	}

	for i := range s.defs {
		if s.defs[i].name == string(name) {
			return s.defs[i].root
		}
	}

	return bad
}

func (s *Schema) literal(b []byte, st int) (Opcode, int, error) {
	bf := Buffer{code: s.prog.code, src: b, tmp: s.prog.tmp}

	val, i, err := bf.value(b, st, false)

	s.prog.code = bf.code
	s.prog.tmp = bf.tmp

	return val, i, err
}

func typeBit(name []byte) int {
	switch string(name) {
	case "null":
		return typeNull
	case "boolean":
		return typeBool
	case "integer":
		return typeInt
	case "number":
		return typeNum
	case "string":
		return typeStr
	case "array":
		return typeArr
	case "object":
		return typeObj
	default:
		return 0
	}
}

func knownKeyword(name []byte) bool {
	switch string(name) {
	case "$schema", "$id", "$anchor", "$comment", "$vocabulary",
		"title", "description", "examples", "readOnly", "writeOnly", "deprecated":
		return true
	case "if", "then", "else", "contains", "minContains", "maxContains",
		"patternProperties", "propertyNames", "prefixItems",
		"dependentSchemas", "dependentRequired", "dependencies",
		"unevaluatedItems", "unevaluatedProperties":
		return true
	case "format", "contentEncoding", "contentMediaType", "contentSchema":
		return true
	case "$dynamicRef", "$dynamicAnchor", "$recursiveRef", "$recursiveAnchor":
		return true
	}

	return len(name) >= 2 && name[0] == 'x' && name[1] == '-'
}

var keywordOrder = []Opcode{
	Ref,
	Type, Enum, Const,
	Minimum, Maximum, ExclMin, ExclMax, MultipleOf,
	MinLen, MaxLen, Pattern,
	MinItems, MaxItems, Unique, Items,
	MinProps, MaxProps, Properties, Required, Additional,
	Not, AllOf, AnyOf, OneOf,
	Default,
}

func (s *Schema) canonRequired(and []Opcode) {
	var props, req Opcode

	for _, op := range and {
		switch op.Op() {
		case Properties:
			props = op
		case Required:
			req = op
		}
	}

	if props.Op() != Properties || req.Op() != Required {
		return
	}

	names := s.prog.Nodes(req)

	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && s.propIndex(props, names[j]) < s.propIndex(props, names[j-1]); j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
}

func (s *Schema) propIndex(props, name Opcode) int {
	off, n := props.Off(), props.Arg()

	for i := range n {
		if string(s.prog.Span(s.prog.code[off+2*i])) == string(s.prog.Span(name)) {
			return i
		}
	}

	return n
}

func sortKeywords(and []Opcode) {
	for i := 1; i < len(and); i++ {
		for j := i; j > 0 && keywordRank(and[j]) < keywordRank(and[j-1]); j-- {
			and[j], and[j-1] = and[j-1], and[j]
		}
	}
}

func keywordRank(op Opcode) int {
	for i, k := range keywordOrder {
		if k == op.Op() {
			return i
		}
	}

	return len(keywordOrder)
}
