package schema

import (
	"bytes"
	"strconv"
)

var typeNames = []struct {
	bit  int
	name string
}{
	{typeNull, "null"},
	{typeBool, "boolean"},
	{typeInt, "integer"},
	{typeNum, "number"},
	{typeStr, "string"},
	{typeArr, "array"},
	{typeObj, "object"},
}

// Format reconstructs the schema document from the program in canonical form.
// $defs round-trips from its Defs node in the tree; the s.defs table is only a
// $ref resolution index and is never emitted from here.
func (s *Schema) Format(w []byte) []byte {
	return s.format(w, s.root)
}

// appendRef writes a ref pointer, collapsing the legacy definitions prefix.
func appendRef(w, p []byte) []byte {
	const legacy = "#/definitions/"

	if bytes.HasPrefix(p, []byte(legacy)) {
		w = append(w, "#/$defs/"...)
		return append(w, p[len(legacy):]...)
	}

	return append(w, p...)
}

func (s *Schema) format(w []byte, op Opcode) []byte {
	switch op.Op() {
	case Pass:
		return append(w, "true"...)
	case Fail:
		return append(w, "false"...)
	case And:
		off, n := op.Off(), op.Arg()

		w = append(w, '{')

		for i := range n {
			c := s.prog.code[off+i]

			if i != 0 {
				w = append(w, ',')
			}

			if c.Op() == Raw {
				w = s.lit(w, s.prog.code[c.Off()])
				w = append(w, ':')
				w = s.lit(w, s.prog.code[c.Off()+1])
				continue
			}

			if c.Op() == CallExt {
				w = append(w, `"x-`...)
				w = append(w, s.xhooks[c.Arg()].name...)
				w = append(w, '"', ':')
				w = s.lit(w, s.prog.code[c.Off()])
				continue
			}

			w = append(w, '"')
			w = append(w, keywordName(c.Op())...)
			w = append(w, '"', ':')
			w = s.constraint(w, c)
		}

		return append(w, '}')
	default:
		panic(op)
	}
}

func (s *Schema) constraint(w []byte, op Opcode) []byte {
	switch op.Op() {
	case Type:
		return s.formatType(w, op.Imm())
	case Properties, Defs:
		off, n := op.Off(), op.Arg()

		w = append(w, '{')

		for i := range n {
			if i != 0 {
				w = append(w, ',')
			}

			w = s.lit(w, s.prog.code[off+2*i])
			w = append(w, ':')
			w = s.format(w, s.prog.code[off+2*i+1])
		}

		return append(w, '}')
	case Required, Enum:
		off, n := op.Off(), op.Arg()

		w = append(w, '[')

		for i := range n {
			if i != 0 {
				w = append(w, ',')
			}

			w = s.lit(w, s.prog.code[off+i])
		}

		return append(w, ']')
	case AllOf, AnyOf, OneOf:
		off, n := op.Off(), op.Arg()

		w = append(w, '[')

		for i := range n {
			if i != 0 {
				w = append(w, ',')
			}

			w = s.format(w, s.prog.code[off+i])
		}

		return append(w, ']')
	case Const, Default, Minimum, Maximum, ExclMin, ExclMax, MultipleOf:
		return s.lit(w, s.prog.code[op.Off()])
	case Additional:
		_, _, sub := s.additionalParts(op)
		return s.format(w, sub)
	case PatternProps:
		off, n := op.Off(), op.Arg()

		w = append(w, '{')

		for i := range n {
			if i != 0 {
				w = append(w, ',')
			}

			w = append(w, s.prog.Span(s.prog.code[off+2*i])...)
			w = append(w, ':')
			w = s.format(w, s.prog.code[off+2*i+1])
		}

		return append(w, '}')
	case Items, Not:
		return s.format(w, s.prog.code[op.Off()])
	case MinLen, MaxLen, MinItems, MaxItems, MinProps, MaxProps:
		return strconv.AppendInt(w, int64(op.Imm()), 10)
	case Unique:
		return append(w, "true"...)
	case Pattern:
		return append(w, s.prog.Span(op)...)
	case Ref:
		w = append(w, '"')
		w = appendRef(w, s.prog.Span(op))
		return append(w, '"')
	default:
		panic(op)
	}
}

// lit renders a value literal, reusing the data encoder over the program arena.
func (s *Schema) lit(w []byte, val Opcode) []byte {
	bf := Buffer{code: s.prog.code, src: s.prog.src}
	return bf.AppendJSON(w, val)
}

func (s *Schema) formatType(w []byte, mask int) []byte {
	one := mask != 0 && mask&(mask-1) == 0
	if !one {
		w = append(w, '[')
	}

	first := true

	for _, t := range typeNames {
		if mask&t.bit == 0 {
			continue
		}

		if !first {
			w = append(w, ',')
		}

		first = false

		w = append(w, '"')
		w = append(w, t.name...)
		w = append(w, '"')
	}

	if !one {
		w = append(w, ']')
	}

	return w
}

func keywordName(op Opcode) string {
	switch op {
	case Type:
		return "type"
	case Properties:
		return "properties"
	case Defs:
		return "$defs"
	case PatternProps:
		return "patternProperties"
	case Required:
		return "required"
	case Enum:
		return "enum"
	case Const:
		return "const"
	case Default:
		return "default"
	case Minimum:
		return "minimum"
	case Maximum:
		return "maximum"
	case ExclMin:
		return "exclusiveMinimum"
	case ExclMax:
		return "exclusiveMaximum"
	case MultipleOf:
		return "multipleOf"
	case Items:
		return "items"
	case Additional:
		return "additionalProperties"
	case Not:
		return "not"
	case AllOf:
		return "allOf"
	case AnyOf:
		return "anyOf"
	case OneOf:
		return "oneOf"
	case MinLen:
		return "minLength"
	case MaxLen:
		return "maxLength"
	case MinItems:
		return "minItems"
	case MaxItems:
		return "maxItems"
	case MinProps:
		return "minProperties"
	case MaxProps:
		return "maxProperties"
	case Unique:
		return "uniqueItems"
	case Pattern:
		return "pattern"
	case Ref:
		return "$ref"
	default:
		panic(op)
	}
}
