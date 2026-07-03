package schema

import (
	"nikand.dev/go/json2"
	"nikand.dev/go/skip"
)

type (
	// Buffer stores decoded value nodes and their bytes. Read through Reader,
	// write through Writer — the two thin wrappers split the value API so a
	// signature says which half it needs.
	Buffer struct {
		code []Opcode // value node arena
		src  []byte   // input bytes, spans point here
		text []byte   // produced scalars

		tmp []Opcode // decode scratch

		textbuf [16]byte
	}

	// BufferReader is the read-only face of a Buffer.
	BufferReader struct{ *Buffer }

	// BufferWriter is the writable face of a Buffer; it appends nodes and bytes.
	BufferWriter struct{ *Buffer }
)

func (b *Buffer) Reader() BufferReader { return BufferReader{b} }
func (b *Buffer) Writer() BufferWriter { return BufferWriter{b} }

func (b BufferWriter) FromJSON(r []byte) (Opcode, error) {
	return b.Buffer.valueFull(r, true)
}

func (b BufferWriter) DecodeJSON(r []byte, st int) (Opcode, int, error) {
	return b.value(r, st, true)
}

func (b *Buffer) decode(r []byte) (Opcode, error) {
	b.src = r
	b.code = b.code[:0]
	b.text = b.text[:0]

	if b.text == nil {
		b.text = b.textbuf[:]
	}

	return b.valueFull(r, false)
}

func (b *Buffer) valueFull(r []byte, intern bool) (Opcode, error) {
	var d json2.Iterator

	val, i, err := b.value(r, 0, intern)
	if err != nil {
		return 0, normSyntax(err)
	}

	i = d.SkipSpaces(r, i)
	if i != len(r) {
		return 0, ErrTrailingData
	}

	return val, nil
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
			return b.Writer().Span(op, r[i:j]), j, nil
		}

		return makeNode(op, i, j-i), j, nil
	case json2.Null:
		j, err := d.Skip(r, i)
		if err != nil {
			return 0, j, err
		}

		return makeNode(Null, i, j-i), j, nil
	case json2.Bool:
		op := False
		if r[i] == 't' {
			op = True
		}

		j, err := d.Skip(r, i)
		if err != nil {
			return 0, j, err
		}

		return makeNode(op, i, j-i), j, nil
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

	return b.Writer().Nodes(Array, b.tmp[mark:], st, i), i, nil
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

	return b.Writer().Nodes(Object, b.tmp[mark:], st, i), i, nil
}

func (b BufferWriter) Span(op Opcode, s []byte) Opcode {
	off := len(b.src) + len(b.text)
	b.text = append(b.text, s...)

	return makeNode(op.Op(), off, len(s))
}

func (b BufferWriter) String(s []byte) Opcode {
	var e json2.Emitter

	off := len(b.src) + len(b.text)
	b.text = e.AppendString(b.text, s)

	return makeNode(Str, off, len(b.src)+len(b.text)-off)
}

// Nodes assembles nodes into a fresh container of kind cont (Array or Object) in
// b. When st >= 0 it parks the source span [st,end) just before the nodes,
// readable via span; pass st < 0 for a synthesized value with no source. The
// span rides one SrcSpan word, or two SrcOff words (start, end) when it is too
// wide for the len field.
func (b BufferWriter) Nodes(cont Opcode, nodes []Opcode, st, end int) Opcode {
	if st >= 0 {
		if n := end - st; n >= 0 && n <= maxArg {
			b.code = append(b.code, makeNode(SrcSpan, st, n))
		} else {
			b.code = append(b.code, makeImm(SrcOff, st), makeImm(SrcOff, end))
		}
	}

	off := len(b.code)
	b.code = append(b.code, nodes...)

	n := len(nodes)
	if cont.Op() == Object {
		n /= 2
	}

	return makeNode(cont.Op(), off, n)
}

// Array assembles elems into a fresh array value in b.
func (b BufferWriter) Array(elems ...Opcode) Opcode { return b.Nodes(Array, elems, -1, -1) }

// Object assembles alternating key/value words into a fresh object value in b.
func (b BufferWriter) Object(kv ...Opcode) Opcode { return b.Nodes(Object, kv, -1, -1) }

func (b BufferWriter) CopyFrom(src BufferReader, op Opcode) Opcode {
	switch op.Op() {
	case Null, True, False:
		return op
	case Num, Str:
		return b.Span(op, src.Span(op))
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

func (b BufferReader) AppendJSON(w []byte, val Opcode) []byte {
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

func (b BufferReader) Span(op Opcode) []byte {
	off, end := b.span(op)

	// Spans resolve a virtual src++text concat: bytes below len(src) live in the
	// read-only input, the rest in the produced-scalar tail.
	if off < len(b.src) {
		return b.src[off:end]
	}

	return b.text[off-len(b.src) : end-len(b.src)]
}

func (b BufferReader) span(op Opcode) (off, end int) {
	switch op.Op() {
	case Num, Str, Null, False, True, Pattern, Ref:
		return op.SpanInt()
	case Object, Array:
	default:
		panic(op.Op())
	}

	idx := op.OffInt()
	if idx-1 >= 0 && b.code[idx-1].Op() == SrcSpan {
		return b.code[idx-1].SpanInt()
	}
	if idx-2 >= 0 && b.code[idx-2].Op() == SrcOff && b.code[idx-1].Op() == SrcOff {
		return b.code[idx-2].ImmInt(), b.code[idx-1].ImmInt()
	}

	return 0, 0
}

// Nodes unwraps block node. Result slice is owned by Buffer.
func (b BufferReader) Nodes(op Opcode) []Opcode {
	off, n := op.Off(), op.Arg()

	switch op.Op() {
	case Object, Properties, PatternProps, Defs:
		n *= 2 // pair-blocks: key/pattern + subschema per entry
	}

	return b.code[off : off+n]
}

// String returns decoded string as bytes.
// Result lifetime is until any other method of that buffer is called.
func (b BufferReader) String(op Opcode) []byte {
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

func (b BufferReader) DecodeString(op Opcode, buf []byte) ([]byte, error) {
	if op.Op() != Str {
		panic(op)
	}

	var d json2.Iterator

	sp := b.Span(op)

	buf, _, err := d.DecodeString(sp, 0, buf)
	return buf, err
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
func (op Opcode) Imm() int64 { return int64(op >> argShift & immMask) }
func (op Opcode) Arg() int64 { return int64(op >> argShift & argMask) }
func (op Opcode) Off() int64 { return int64(op >> offShift & offMask) }

// OffInt, ArgInt, and ImmInt narrow the accessors to int for indexing and
// lengths; the payload fields are far below math.MaxInt on any real program.
func (op Opcode) OffInt() int { return int(op.Off()) }
func (op Opcode) ArgInt() int { return int(op.Arg()) }
func (op Opcode) ImmInt() int { return int(op.Imm()) }

func (op Opcode) SpanInt() (off, end int) { off = op.OffInt(); return off, off + op.ArgInt() }
