package schema

import (
	"nikand.dev/go/json2"
	"nikand.dev/go/skip"
)

type (
	Buffer struct {
		code []Opcode // value node arena
		src  []byte   // input bytes, spans point here
		text []byte   // produced scalars

		tmp []Opcode // decode scratch

		textbuf [16]byte

		ro bool
	}
)

func (b *Buffer) decode(r []byte) (Opcode, error) {
	b.readOnly()

	b.src = r
	b.code = b.code[:0]
	b.text = b.text[:0]

	if b.text == nil {
		b.text = b.textbuf[:]
	}

	var d json2.Iterator

	val, i, err := b.value(r, 0, false)
	if err != nil {
		return 0, err
	}

	i = d.SkipSpaces(r, i)
	if i != len(r) {
		return 0, json2.ErrSyntax
	}

	return val, nil
}

func (b *Buffer) FromJSON(r []byte) (Opcode, error) {
	var d json2.Iterator

	val, i, err := b.DecodeJSON(r, 0)
	if err != nil {
		return 0, err
	}

	i = d.SkipSpaces(r, i)
	if i != len(r) {
		return 0, json2.ErrSyntax
	}

	return val, nil
}

func (b *Buffer) DecodeJSON(r []byte, st int) (Opcode, int, error) {
	return b.value(r, st, true)
}

func (b *Buffer) value(r []byte, st int, intern bool) (val Opcode, i int, err error) {
	var d json2.Iterator

	tp, i, err := d.Type(r, st)
	if err != nil {
		return 0, i, err
	}

	switch tp {
	case json2.Object:
		return b.object(r, i, intern)
	case json2.Array:
		return b.array(r, i, intern)
	case json2.String, json2.Number:
		op := Str
		if tp == json2.Number {
			op = Num
		}

		j, err := d.Skip(r, i)
		if err != nil {
			return 0, j, err
		}

		if intern {
			return b.EmitSpan(op, r[i:j]), j, nil
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

func (b *Buffer) array(r []byte, st int, intern bool) (Opcode, int, error) {
	mark := len(b.tmp)
	defer func() { b.tmp = b.tmp[:mark] }()

	var d json2.Iterator

	i, err := d.Enter(r, st, json2.Array)
	if err != nil {
		return 0, i, err
	}

	var val Opcode

	for d.ForMore(r, &i, json2.Array, &err) {
		val, i, err = b.value(r, i, intern)
		if err != nil {
			return 0, i, err
		}

		b.tmp = append(b.tmp, val)
	}
	if err != nil {
		return 0, i, err
	}

	return b.Array(b.tmp[mark:]...), i, nil
}

func (b *Buffer) object(r []byte, st int, intern bool) (Opcode, int, error) {
	mark := len(b.tmp)
	defer func() { b.tmp = b.tmp[:mark] }()

	var d json2.Iterator

	i, err := d.Enter(r, st, json2.Object)
	if err != nil {
		return 0, i, err
	}

	var key, val Opcode

	for d.ForMore(r, &i, json2.Object, &err) {
		key, i, err = b.value(r, i, intern)
		if err != nil {
			return 0, i, err
		}

		val, i, err = b.value(r, i, intern)
		if err != nil {
			return 0, i, err
		}

		b.tmp = append(b.tmp, key, val)
	}
	if err != nil {
		return 0, i, err
	}

	return b.Object(b.tmp[mark:]...), i, nil
}

func (b *Buffer) EmitSpan(op Opcode, s []byte) Opcode {
	b.readOnly()

	off := len(b.src) + len(b.text)
	b.text = append(b.text, s...)

	return makeNode(op.Op(), off, len(s))
}

func (b *Buffer) EmitString(s []byte) Opcode {
	b.readOnly()

	var e json2.Emitter

	off := len(b.src) + len(b.text)
	b.text = e.AppendString(b.text, s)

	return makeNode(Str, off, len(b.src)+len(b.text)-off)
}

// Array assembles elems into a fresh array value in b.
func (b *Buffer) Array(elems ...Opcode) Opcode {
	b.readOnly()

	off := len(b.code)
	b.code = append(b.code, elems...)

	return makeNode(Array, off, len(elems))
}

// Object assembles alternating key/value words into a fresh object value in b.
func (b *Buffer) Object(kv ...Opcode) Opcode {
	b.readOnly()

	off := len(b.code)
	b.code = append(b.code, kv...)

	return makeNode(Object, off, len(kv)/2)
}

func (b *Buffer) CopyFrom(src *Buffer, op Opcode) Opcode {
	b.readOnly()

	switch op.Op() {
	case Null, True, False:
		return op
	case Num, Str:
		return b.EmitSpan(op, src.Span(op))
	case Array, Object:
		mark := len(b.tmp)
		defer func() { b.tmp = b.tmp[:mark] }()

		for _, ch := range src.Nodes(op) {
			b.tmp = append(b.tmp, b.CopyFrom(src, ch))
		}

		if op.Op() == Object {
			return b.Object(b.tmp[mark:]...)
		}

		return b.Array(b.tmp[mark:]...)
	default:
		panic(op)
	}
}

func (b *Buffer) AppendJSON(w []byte, val Opcode) []byte {
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

			w = b.AppendJSON(w, b.code[voff+i])
		}

		return append(w, ']')
	case Object:
		voff, vn := val.Off(), val.Arg()

		w = append(w, '{')

		for i := range vn {
			if i != 0 {
				w = append(w, ',')
			}

			w = b.AppendJSON(w, b.code[voff+2*i])
			w = append(w, ':')
			w = b.AppendJSON(w, b.code[voff+2*i+1])
		}

		return append(w, '}')
	default:
		panic(val)
	}
}

func (b *Buffer) Span(op Opcode) []byte {
	off, n := op.Off(), op.Arg()

	if off < len(b.src) {
		return b.src[off : off+n]
	}

	off -= len(b.src)

	return b.text[off : off+n]
}

func (b *Buffer) Nodes(op Opcode) []Opcode {
	off, n := op.Off(), op.Arg()
	if op.Op() == Object || op.Op() == Properties {
		n *= 2
	}

	return b.code[off : off+n]
}

// String returns decoded string as bytes.
// Result lifetime is until any other method of that buffer is called.
func (b *Buffer) String(op Opcode) []byte {
	if op.Op() != Str {
		panic(op)
	}

	sp := b.Span(op)

	s, _, _, _ := skip.String(sp, 0, skip.Dqt)
	if s.Err() {
		return nil
	}

	if !s.Is(skip.Escapes) {
		return sp[1 : len(sp)-1]
	}

	mark := len(b.text)
	defer func() { b.text = b.text[:mark] }()

	s, b.text, _, _ = skip.DecodeString(sp, 0, skip.Dqt|skip.StrEscapes, b.text)
	if s.Err() {
		return nil
	}

	return b.text[mark:]
}

func (b *Buffer) DecodeString(op Opcode, buf []byte) ([]byte, error) {
	if op.Op() != Str {
		panic(op)
	}

	var d json2.Iterator

	sp := b.Span(op)

	buf, _, err := d.DecodeString(sp, 0, buf)
	return buf, err
}

func (b *Buffer) readOnly() {
	if b.ro {
		panic("read only")
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
