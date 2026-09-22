package schema

import (
	"bytes"
	"strconv"

	"nikand.dev/go/json2"
)

// Format reconstructs the schema document from the program in canonical form.
// $defs round-trips from its Defs node in the tree; the s.defs table is only a
// $ref resolution index and is never emitted from here.
func (s *Schema) Format(w []byte) []byte {
	return s.format(w, s.root)
}

// FormatNode renders a single program node (e.g. a subschema reached via Root
// and SchemaBuf) as schema JSON.
func (s *Schema) FormatNode(w []byte, op Opcode) []byte {
	return s.format(w, op)
}

// FormatKeyword renders a keyword node's value as schema JSON: the operand a
// Diag.Op stands for (3 for minLength, ["integer","null"] for type, the name
// for a required entry, the subschema for additionalProperties).
func (s *Schema) FormatKeyword(w []byte, op Opcode) []byte {
	switch op.Op() {
	case String, Key:
		return s.lit(w, op)
	case Raw, Ext:
		return s.lit(w, s.prog.code[op.Off()+1])
	case Pass, Fail:
		return s.format(w, op)
	default:
		return s.constraint(w, op)
	}
}

// appendRef writes a ref pointer, collapsing the legacy definitions prefix.
// appendRef emits a $ref pointer, rewriting the legacy $defs location on the way
// out. The pointer is a string like any other, so it is encoded, not appended.
func appendRef(w, p []byte) []byte {
	const legacy = "#/definitions/"

	var e json2.Emitter

	w = append(w, '"')

	if bytes.HasPrefix(p, []byte(legacy)) {
		w = e.AppendStringContent(w, []byte("#/$defs/"))
		p = p[len(legacy):]
	}

	w = e.AppendStringContent(w, p)

	return append(w, '"')
}

func (s *Schema) dump(w []byte, op Opcode) []byte {
	switch op.Op() {
	case Pass, Fail, All:
		return s.format(w, op)
	default:
		return s.constraint(w, op)
	}
}

func (s *Schema) format(w []byte, op Opcode) []byte {
	switch op.Op() {
	case Pass:
		return append(w, "true"...)
	case Fail:
		return append(w, "false"...)
	case All:
		off, n := op.Off(), op.Arg()

		w = append(w, '{')

		for i := range n {
			c := s.prog.code[off+i]

			if i != 0 {
				w = append(w, ',')
			}

			if c.Op() == Raw || c.Op() == Ext {
				w = s.lit(w, s.prog.code[c.Off()])
				w = append(w, ':')
				w = s.lit(w, s.prog.code[c.Off()+1])
				continue
			}

			w = append(w, '"')
			w = append(w, c.Keyword()...)
			w = append(w, '"', ':')
			w = s.constraint(w, c)
		}

		return append(w, '}')
	default:
		panic(op.Op())
	}
}

func (s *Schema) constraint(w []byte, op Opcode) []byte {
	switch op.Op() {
	case Type:
		return s.formatType(w, TypesOf(op))
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
	case AllOf, AnyOf, OneOf, Prefix:
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

		var e json2.Emitter

		w = append(w, '{')

		for i := range n {
			if i != 0 {
				w = append(w, ',')
			}

			w = e.AppendString(w, s.prog.Reader().Span(s.prog.code[off+2*i]))
			w = append(w, ':')
			w = s.format(w, s.prog.code[off+2*i+1])
		}

		return append(w, '}')
	case Items, Not, Then, Else:
		return s.format(w, s.prog.code[op.Off()])
	case If:
		cond, _, _ := s.condParts(op)
		return s.format(w, cond)
	case MinLen, MaxLen, MinItems, MaxItems, MinProps, MaxProps:
		return strconv.AppendInt(w, op.Imm(), 10)
	case Unique:
		if op.Imm() == 0 {
			return append(w, "false"...)
		}

		return append(w, "true"...)
	case Format:
		return append(append(append(w, '"'), formatNames[op.Imm()]...), '"')
	case Pattern:
		var e json2.Emitter

		return e.AppendString(w, s.prog.Reader().Span(op))
	case Ref:
		return appendRef(w, s.prog.Reader().Span(op))
	default:
		panic(op.Op())
	}
}

// lit renders a value literal, reusing the data encoder over the program arena.
func (s *Schema) lit(w []byte, val Opcode) []byte {
	return s.prog.Reader().AppendJSON(w, val)
}

func (s *Schema) formatType(w []byte, mask Types) []byte {
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

// Keyword is the schema keyword a node stands for, "" for a node that is not a
// keyword (a value, a required entry, a bare schema).
func (op Opcode) Keyword() string {
	switch op.Op() {
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
	case Prefix:
		return "prefixItems"
	case Additional:
		return "additionalProperties"
	case Not:
		return "not"
	case If:
		return "if"
	case Then:
		return "then"
	case Else:
		return "else"
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
	case Format:
		return "format"
	case Ref:
		return "$ref"
	default:
		return ""
	}
}
