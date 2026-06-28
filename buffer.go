package schema

import "nikand.dev/go/json2"

type (
	Buffer struct {
		code []Opcode // value node arena
		src  []byte   // input bytes, spans point here
		text []byte   // produced scalars

		tmp []Opcode // decode scratch
	}
)

func (b *Buffer) decode(r []byte) (Opcode, error) {
	b.src = r
	b.code = b.code[:0]
	b.text = b.text[:0]

	var d json2.Iterator

	val, i, err := b.value(r, 0)
	if err != nil {
		return 0, err
	}

	i = d.SkipSpaces(r, i)
	if i != len(r) {
		return 0, json2.ErrSyntax
	}

	return val, nil
}

func (b *Buffer) value(r []byte, st int) (val Opcode, i int, err error) {
	var d json2.Iterator

	tp, i, err := d.Type(r, st)
	if err != nil {
		return 0, i, err
	}

	switch tp {
	case json2.Object:
		return b.object(r, i)
	case json2.Array:
		return b.array(r, i)
	case json2.String, json2.Number:
		op := Str
		if tp == json2.Number {
			op = Num
		}

		j, err := d.Skip(r, i)
		if err != nil {
			return 0, j, err
		}

		return makeNode(op, i, j-i), j, nil
	case json2.Null:
		i, err = d.Skip(r, i)
		return Null, i, err
	case json2.Bool:
		val = False
		if r[i] == 't' {
			val = True
		}

		i, err = d.Skip(r, i)
		return val, i, err
	default:
		return 0, i, json2.ErrSyntax
	}
}

func (b *Buffer) array(r []byte, st int) (Opcode, int, error) {
	mark := len(b.tmp)
	defer func() { b.tmp = b.tmp[:mark] }()

	var d json2.Iterator

	i, err := d.Enter(r, st, json2.Array)
	if err != nil {
		return 0, i, err
	}

	var val Opcode

	for d.ForMore(r, &i, json2.Array, &err) {
		val, i, err = b.value(r, i)
		if err != nil {
			return 0, i, err
		}

		b.tmp = append(b.tmp, val)
	}
	if err != nil {
		return 0, i, err
	}

	n := len(b.tmp) - mark
	off := len(b.code)
	b.code = append(b.code, b.tmp[mark:]...)

	return makeNode(Array, off, n), i, nil
}

func (b *Buffer) object(r []byte, st int) (Opcode, int, error) {
	mark := len(b.tmp)
	defer func() { b.tmp = b.tmp[:mark] }()

	var d json2.Iterator

	i, err := d.Enter(r, st, json2.Object)
	if err != nil {
		return 0, i, err
	}

	var key, val Opcode

	for d.ForMore(r, &i, json2.Object, &err) {
		key, i, err = b.value(r, i)
		if err != nil {
			return 0, i, err
		}

		val, i, err = b.value(r, i)
		if err != nil {
			return 0, i, err
		}

		b.tmp = append(b.tmp, key, val)
	}
	if err != nil {
		return 0, i, err
	}

	n := (len(b.tmp) - mark) / 2
	off := len(b.code)
	b.code = append(b.code, b.tmp[mark:]...)

	return makeNode(Object, off, n), i, nil
}

func (b *Buffer) encode(w []byte, val Opcode) []byte {
	switch val.Op() {
	case Null:
		return append(w, "null"...)
	case True:
		return append(w, "true"...)
	case False:
		return append(w, "false"...)
	case Num, Str:
		return append(w, b.Span(val)...)
	case Array:
		voff, vn := val.Off(), val.Arg()

		w = append(w, '[')

		for i := range vn {
			if i != 0 {
				w = append(w, ',')
			}

			w = b.encode(w, b.code[voff+i])
		}

		return append(w, ']')
	case Object:
		voff, vn := val.Off(), val.Arg()

		w = append(w, '{')

		for i := range vn {
			if i != 0 {
				w = append(w, ',')
			}

			w = b.encode(w, b.code[voff+2*i])
			w = append(w, ':')
			w = b.encode(w, b.code[voff+2*i+1])
		}

		return append(w, '}')
	default:
		panic(val)
	}
}

func makeNode(op Opcode, off, n int) Opcode {
	if off < 0 || off > maxOff {
		panic(off)
	}
	if n < 0 || n > maxArg {
		panic(n)
	}

	return op | Opcode(n)<<argShift | Opcode(off)<<offShift
}

func makeImm(op Opcode, v int) Opcode {
	if v < 0 || v > maxImm {
		panic(v)
	}

	return op | Opcode(v)<<argShift
}

func (op Opcode) Op() Opcode { return op & opMask }
func (op Opcode) Imm() int   { return int(op >> argShift) }
func (op Opcode) Arg() int   { return int(op >> argShift & maxArg) }
func (op Opcode) Off() int   { return int(op >> offShift) }

// Nodes returns a block's child words: an Array's n elements, an Object's n
// key/value pairs (2n words), or a keyword block's operands — what a handler
// iterates to walk into a container. Object and Properties count pairs.
func (b *Buffer) Nodes(op Opcode) []Opcode {
	off, n := op.Off(), op.Arg()
	if op.Op() == Object || op.Op() == Properties {
		n *= 2
	}

	return b.code[off : off+n]
}

// Span resolves a scalar's bytes across the input and synthesized-text tail:
// off below len(src) is original input, above is appended during rewrite.
func (b *Buffer) Span(op Opcode) []byte {
	off, n := op.Off(), op.Arg()

	if off < len(b.src) {
		return b.src[off : off+n]
	}

	off -= len(b.src)

	return b.text[off : off+n]
}
