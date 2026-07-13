package schema

import (
	"iter"
	"math"
	"strconv"

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
		opbuf   [46]Opcode // size is tuned for allocation span buckets
	}

	// BufferReader is the read-only face of a Buffer.
	BufferReader struct{ *Buffer }

	// BufferWriter is the writable face of a Buffer; it appends nodes and bytes.
	BufferWriter struct{ *Buffer }
)

func (b *Buffer) Reader() BufferReader { return BufferReader{b} }
func (b *Buffer) Writer() BufferWriter { return BufferWriter{b} }

func (b *Buffer) Reset() {
	if b.tmp == nil {
		b.tmp = b.opbuf[:10:10]
	}
	if b.code == nil {
		b.code = b.opbuf[10:]
	}
	if b.text == nil {
		b.text = b.textbuf[:]
	}

	b.code = b.code[:0]
	b.text = b.text[:0]
	b.tmp = b.tmp[:0]
}

func (b *Buffer) decode(r []byte) (Opcode, error) {
	b.src = r

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
		op := String
		if tp == json2.Number {
			op = Number
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

func (b BufferWriter) FromJSON(r []byte) (Opcode, error) {
	return b.Buffer.valueFull(r, true)
}

func (b BufferWriter) DecodeJSON(r []byte, st int) (Opcode, int, error) {
	return b.value(r, st, true)
}

func (b BufferWriter) Span(op Opcode, s []byte) Opcode {
	off := len(b.src) + len(b.text)
	b.text = append(b.text, s...)

	return makeNode(op.Op(), off, len(s))
}

func (b BufferWriter) Bytes(s []byte) Opcode {
	var e json2.Emitter

	off := len(b.src) + len(b.text)
	b.text = e.AppendString(b.text, s)

	return makeNode(String, off, len(b.src)+len(b.text)-off)
}

func (b BufferWriter) String(s string) Opcode {
	return b.Bytes([]byte(s))
}

func (b BufferWriter) Int(x int) Opcode {
	return MakeInt(int64(x))
}

func (b BufferWriter) Int64(x int64) Opcode {
	return MakeInt(x)
}

func (b BufferWriter) Float(x float64) Opcode {
	return MakeFlt(x)
}

func (b BufferWriter) Bool(x bool) Opcode {
	if x {
		return True
	}

	return False
}

func (b BufferWriter) Null() Opcode {
	return Null
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
	case Number, String:
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
	case Number, String:
		return append(w, b.Span(val)...)
	case IntLit:
		return strconv.AppendInt(w, val.Imm(), 10)
	case FltLit:
		return strconv.AppendFloat(w, val.Flt(), 'f', -1, 64)
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
	case Number, String, Null, False, True, Pattern, Ref, Key:
		return op.SpanInt()
	case None:
		return 0, 0
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

// Nodes unwraps a container node into its child words. Result slice is owned by
// Buffer. Panics on a node that is not a container (single-child pointers like
// Not/Items/Const are references, not containers).
func (b BufferReader) Nodes(op Opcode) []Opcode {
	off, n := op.OffInt(), op.ArgInt()

	switch op.Op() {
	case Object, Properties, PatternProps, Defs, Raw, Ext:
		n *= 2 // pair-blocks: key + subschema per entry
	case All, AllOf, AnyOf, OneOf, Enum, Required, Array:
		// list-blocks: one child per entry
	default:
		panic(op.Op())
	}

	return b.code[off : off+n]
}

func (b BufferReader) NodesLen(op Opcode) int {
	return op.ArgInt()
}

func (b BufferReader) NodesAt(op Opcode, i int) (k, v Opcode) {
	off, n := op.OffInt(), op.ArgInt()
	if i < 0 {
		i = n + i
	}
	if i >= n || i < 0 {
		return None, None
	}

	switch op.Op() {
	case Object, Properties, PatternProps, Defs, Raw, Ext:
		i *= 2 // kv-pairs

		return b.code[off+i], b.code[off+i+1]
	case All, AllOf, AnyOf, OneOf, Enum, Required, Array:
		return MakeInt(int64(i)), b.code[off+i]
	default:
		panic(op.Op())
	}
}

// Ext returns the value node of the extension keyword named key (e.g. "x-type")
// among the keywords of schema node op (an All), or None if absent. The key is
// matched whole, so any extension prefix falls into the same path. The value is
// left to the caller to interpret; None is never a valid value.
func (b BufferReader) Ext(op Opcode, key string) Opcode {
	if op.Op() != All {
		panic(op.Op())
	}

	for _, ch := range b.Nodes(op) {
		if ch.Op() != Ext {
			continue
		}

		k, v := b.NodesAt(ch, 0)
		if string(b.String(k)) == key {
			return v
		}
	}

	return None
}

// Iter ranges over the children of any node, pairing key with value — the
// generalization of Nodes/NodesAt (pair- and list-blocks), Deref (single-child
// pointers), and the variadic Additional. Pair-blocks yield (key, sub);
// list-blocks yield (IntLit index, elem); single-child nodes yield (None, sub). A
// scalar or in-opcode keyword (Type, MinLen, Pattern, …) has no children.
func (b BufferReader) Iter(op Opcode) iter.Seq2[Opcode, Opcode] {
	off := op.OffInt()

	return func(yield func(k, v Opcode) bool) {
		switch op.Op() {
		case Object, Properties, PatternProps, Defs, Raw, Ext:
			for i := range op.ArgInt() {
				if !yield(b.code[off+2*i], b.code[off+2*i+1]) {
					return
				}
			}
		case All, AllOf, AnyOf, OneOf, Enum, Required, Array:
			for i := range op.ArgInt() {
				if !yield(MakeInt(int64(i)), b.code[off+i]) {
					return
				}
			}
		case Additional:
			if op.ArgInt() == 3 {
				yield(None, b.code[off+2]) // props, patterns, sub — sub only
				return
			}

			yield(None, b.code[off])
		case Not, Items, Const, Default, Minimum, Maximum, ExclMin, ExclMax, MultipleOf:
			yield(None, b.code[off])
		}
	}
}

// Keyword returns the keyword node of kind want among the keywords of schema
// node op (an All), or None if absent. Every keyword is unique per schema, so the
// match is unambiguous — except Ext and Raw, which repeat and are keyed by name;
// look those up with Ext instead. Read the returned node with Deref, Nodes, or
// its Imm, per the keyword.
func (b BufferReader) Keyword(op, want Opcode) Opcode {
	if op.Op() != All {
		panic(op.Op())
	}

	for _, ch := range b.Nodes(op) {
		if ch.Op() == want.Op() {
			return ch
		}
	}

	return None
}

// Deref returns the single child of a pointer node — the subschema or operand it
// points at (Not/Items/Const/Default/Minimum/…). These hold one reference, not a
// list, so it panics on any other op. Additional is variadic — split it with
// additionalParts instead.
func (b BufferReader) Deref(op Opcode) Opcode {
	switch op.Op() {
	case Not, Items, Const, Default, Minimum, Maximum, ExclMin, ExclMax, MultipleOf:
		return b.code[op.OffInt()]
	default:
		panic(op.Op())
	}
}

// String returns decoded string as bytes.
// Result lifetime is until any other method of that buffer is called.
func (b BufferReader) String(op Opcode) []byte {
	if op.Op() != String {
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
	if op.Op() != String {
		panic(op)
	}

	var d json2.Iterator

	sp := b.Span(op)

	buf, _, err := d.DecodeString(sp, 0, buf)
	return buf, err
}

// Int, Int64, and Float read a numeric value node, whether it was decoded from
// JSON text (a Num span) or synthesized in-opcode (an IntLit or FltLit). A
// non-numeric node yields ErrNotNumber; a malformed Num span yields the decoder's
// parse error.
func (b BufferReader) Int(op Opcode) (int, error) {
	v, err := b.Int64(op)
	return int(v), err
}

func (b BufferReader) Int64(op Opcode) (int64, error) {
	switch op.Op() {
	case IntLit:
		return op.Imm(), nil
	case FltLit:
		v := op.Flt()
		if v != math.Trunc(v) {
			return 0, ErrNotInteger
		}

		return int64(v), nil
	case Number:
		sp := b.Span(op)

		// Plain integer literals parse exactly (int64 outranges float64's integers);
		// decimal/exponent forms fall back to a value check: integral or ErrNotInteger.
		if v, err := json2.Value(sp).Int64(); err == nil {
			return v, nil
		}

		v, err := json2.Value(sp).Float64()
		if err != nil {
			return 0, err
		}

		if v != math.Trunc(v) {
			return 0, ErrNotInteger
		}

		return int64(v), nil
	default:
		return 0, ErrNotNumber
	}
}

func (b BufferReader) Float(op Opcode) (float64, error) {
	switch op.Op() {
	case FltLit:
		return op.Flt(), nil
	case IntLit:
		return float64(op.Imm()), nil
	case Number:
		return json2.Value(b.Span(op)).Float64()
	default:
		return 0, ErrNotNumber
	}
}
