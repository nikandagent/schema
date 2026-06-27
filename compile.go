package schema

import (
	"errors"

	"nikand.dev/go/json2"
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

func (s *Schema) Compile(b []byte) error {
	s.schema = b
	s.code = s.code[:0]
	s.tmp = s.tmp[:0]

	var d json2.Iterator

	root, i, err := s.compile(b, 0)
	if err != nil {
		return err
	}

	i = d.SkipSpaces(b, i)
	if i != len(b) {
		return ErrSchema
	}

	s.root = root

	return nil
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
	mark := len(s.tmp)
	defer func() { s.tmp = s.tmp[:mark] }()

	var d json2.Iterator

	i, err := d.Enter(b, st, json2.Object)
	if err != nil {
		return 0, i, err
	}

	var key []byte
	var op Opcode

	for d.ForMore(b, &i, json2.Object, &err) {
		key, i, err = d.Key(b, i)
		if err != nil {
			return 0, i, err
		}

		op, i, err = s.keyword(key, b, i)
		if err != nil {
			return 0, i, err
		}

		if op != Pass {
			s.tmp = append(s.tmp, op)
		}
	}
	if err != nil {
		return 0, i, err
	}

	arg := len(s.tmp) - mark
	off := len(s.code)
	s.code = append(s.code, s.tmp[mark:]...)

	return makeNode(And, off, arg), i, nil
}

func (s *Schema) keyword(name, b []byte, st int) (Opcode, int, error) {
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
	default:
		return s.kwUnknown(name, b, st)
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
	mark := len(s.tmp)
	defer func() { s.tmp = s.tmp[:mark] }()

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

		s.tmp = append(s.tmp, key, sub)
	}
	if err != nil {
		return 0, i, err
	}

	arg := (len(s.tmp) - mark) / 2
	off := len(s.code)
	s.code = append(s.code, s.tmp[mark:]...)

	return makeNode(Properties, off, arg), i, nil
}

func (s *Schema) kwList(op Opcode, b []byte, st int) (Opcode, int, error) {
	mark := len(s.tmp)
	defer func() { s.tmp = s.tmp[:mark] }()

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

		s.tmp = append(s.tmp, val)
	}
	if err != nil {
		return 0, i, err
	}

	arg := len(s.tmp) - mark
	off := len(s.code)
	s.code = append(s.code, s.tmp[mark:]...)

	return makeNode(op, off, arg), i, nil
}

func (s *Schema) kwSchemas(op Opcode, b []byte, st int) (Opcode, int, error) {
	mark := len(s.tmp)
	defer func() { s.tmp = s.tmp[:mark] }()

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

		s.tmp = append(s.tmp, sub)
	}
	if err != nil {
		return 0, i, err
	}

	arg := len(s.tmp) - mark
	off := len(s.code)
	s.code = append(s.code, s.tmp[mark:]...)

	return makeNode(op, off, arg), i, nil
}

func (s *Schema) kwSub(op Opcode, b []byte, st int) (Opcode, int, error) {
	sub, i, err := s.compile(b, st)
	if err != nil {
		return 0, i, err
	}

	off := len(s.code)
	s.code = append(s.code, sub)

	return makeNode(op, off, 1), i, nil
}

func (s *Schema) kwValue(op Opcode, b []byte, st int) (Opcode, int, error) {
	val, i, err := s.literal(b, st)
	if err != nil {
		return 0, i, err
	}

	off := len(s.code)
	s.code = append(s.code, val)

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

// kwUnknown handles keywords outside the compiled set: annotations, deferred
// refs and x- extensions are consumed and ignored; anything else is rejected.
func (s *Schema) kwUnknown(name, b []byte, st int) (Opcode, int, error) {
	if !extraKeyword(name) {
		return 0, st, ErrSchema
	}

	var d json2.Iterator

	i, err := d.Skip(b, st)
	if err != nil {
		return 0, i, err
	}

	// TODO: preserve x- extensions (emit a custom node) instead of dropping them
	return Pass, i, nil
}

// literal decodes a JSON value into the program arena, reusing the data
// decoder. Spans point into the schema source.
func (s *Schema) literal(b []byte, st int) (Opcode, int, error) {
	bf := Buffer{code: s.code, src: b, tmp: s.tmp}

	val, i, err := bf.value(b, st)

	s.code = bf.code
	s.tmp = bf.tmp

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

func extraKeyword(name []byte) bool {
	switch string(name) {
	case "$schema", "$id", "$anchor", "$comment", "title", "description", "examples",
		"$defs", "definitions", "$ref":
		return true
	}

	return len(name) >= 2 && name[0] == 'x' && name[1] == '-'
}
