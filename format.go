package schema

import (
	"bytes"
	"strconv"
	"strings"
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
func (s *Schema) Format(w []byte) []byte {
	w = s.format(w, s.root)

	if len(s.defs) == 0 {
		return w
	}

	w = w[:len(w)-1] // reopen the root object, dropping its closing brace
	if w[len(w)-1] != '{' {
		w = append(w, ',')
	}

	w = append(w, `"$defs":{`...)

	for i := range s.defs {
		if i != 0 {
			w = append(w, ',')
		}

		w = append(w, '"')
		w = append(w, defName(s.defs[i].name)...)
		w = append(w, '"', ':')
		w = s.format(w, s.defs[i].root)
	}

	return append(w, '}', '}')
}

func defName(p string) string {
	if i := strings.LastIndexByte(p, '/'); i >= 0 {
		return p[i+1:]
	}

	return p
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
		off, n := op.off(), op.arg()

		w = append(w, '{')

		for i := range n {
			c := s.code[off+i]

			if i != 0 {
				w = append(w, ',')
			}

			if c.Op() == Raw {
				w = s.lit(w, s.code[c.off()])
				w = append(w, ':')
				w = s.lit(w, s.code[c.off()+1])
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
		return s.formatType(w, op.imm())
	case Properties:
		off, n := op.off(), op.arg()

		w = append(w, '{')

		for i := range n {
			if i != 0 {
				w = append(w, ',')
			}

			w = s.lit(w, s.code[off+2*i])
			w = append(w, ':')
			w = s.format(w, s.code[off+2*i+1])
		}

		return append(w, '}')
	case Required, Enum:
		off, n := op.off(), op.arg()

		w = append(w, '[')

		for i := range n {
			if i != 0 {
				w = append(w, ',')
			}

			w = s.lit(w, s.code[off+i])
		}

		return append(w, ']')
	case AllOf, AnyOf, OneOf:
		off, n := op.off(), op.arg()

		w = append(w, '[')

		for i := range n {
			if i != 0 {
				w = append(w, ',')
			}

			w = s.format(w, s.code[off+i])
		}

		return append(w, ']')
	case Const, Default, Minimum, Maximum, ExclMin, ExclMax, MultipleOf:
		return s.lit(w, s.code[op.off()])
	case Items, Additional, Not:
		return s.format(w, s.code[op.off()])
	case MinLen, MaxLen, MinItems, MaxItems, MinProps, MaxProps:
		return strconv.AppendInt(w, int64(op.imm()), 10)
	case Unique:
		return append(w, "true"...)
	case Pattern:
		return append(w, op.str(s.schema)...)
	case Ref:
		w = append(w, '"')
		w = appendRef(w, op.str(s.schema))
		return append(w, '"')
	default:
		panic(op)
	}
}

// lit renders a value literal, reusing the data encoder over the program arena.
func (s *Schema) lit(w []byte, val Opcode) []byte {
	bf := Buffer{code: s.code, src: s.schema}
	return bf.encode(w, val)
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
