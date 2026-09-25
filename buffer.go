package schema

import (
	"bytes"
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
		code []Node // value node arena
		src  []byte // input bytes, read-only, spans point here
		text []byte // produced scalars, reused private buffer

		tmp []Node // decode scratch

		textbuf [16]byte
		opbuf   [49]Node // sized to fill the 896 bucket: tmp opbuf[:10], code the rest
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

func (b *Buffer) decode(r []byte) (Node, error) {
	b.src = r

	return b.valueFull(r, false)
}

func (b *Buffer) valueFull(r []byte, intern bool) (Node, error) {
	var d json2.Iterator

	val, i, err := b.value(r, 0, intern)
	if err != nil {
		return Node{}, normSyntax(err)
	}

	i = d.SkipSpaces(r, i)
	if i != len(r) {
		return Node{}, ErrTrailingData
	}

	return val, nil
}

func (b *Buffer) value(r []byte, st int, intern bool) (val Node, i int, err error) {
	var d json2.Iterator

	tp, i, err := d.Type(r, st)
	if err != nil {
		return Node{}, i, err
	}

	switch tp {
	case json2.Object:
		return b.object(r, i, intern)
	case json2.Array:
		return b.array(r, i, intern)
	case json2.String, json2.Number:
		j, err := d.Skip(r, i)
		if err != nil {
			return Node{}, j, err
		}

		if tp == json2.Number {
			if intern {
				return b.Writer().Span(Number, r[i:j]).withSrc(i, j), j, nil
			}

			return makeNode(Number, i, j-i).withSrc(i, j), j, nil
		}

		val, err := b.str(r, i, j, String, intern)

		return val, j, err
	case json2.Null:
		j, err := d.Skip(r, i)
		if err != nil {
			return Node{}, j, err
		}

		return makeNode(Null, i, j-i).withSrc(i, j), j, nil
	case json2.Bool:
		op := False
		if r[i] == 't' {
			op = True
		}

		j, err := d.Skip(r, i)
		if err != nil {
			return Node{}, j, err
		}

		return makeNode(op, i, j-i).withSrc(i, j), j, nil
	default:
		return Node{}, i, json2.ErrSyntax
	}
}

// str stores the string token r[i:j] under opcode op. A string node holds the
// string itself, never its spelling, so an unescaped token is the source body
// as it stands and costs no copy; only an escaped one is decoded into the text
// tail, and gives up its source position by moving there.
func (b *Buffer) str(r []byte, i, j int, op Opcode, intern bool) (Node, error) {
	tok := r[i:j]

	if bytes.IndexByte(tok, '\\') < 0 {
		if intern {
			return b.Writer().Span(op, tok[1:len(tok)-1]).withSrc(i, j), nil
		}

		return makeNode(op, i+1, j-i-2).withSrc(i, j), nil
	}

	off := len(b.src) + len(b.text)

	s, text, _, _ := skip.DecodeString(tok, 0, skip.Dqt|skip.StrEscapes, b.text)
	if s.Err() {
		return Node{}, json2.ErrSyntax
	}

	b.text = text

	return makeNode(op, off, len(b.src)+len(b.text)-off).withSrc(i, j), nil
}

func (b *Buffer) array(r []byte, st int, intern bool) (Node, int, error) {
	mark := len(b.tmp)
	defer func() { b.tmp = b.tmp[:mark] }()

	var d json2.Iterator

	i, err := d.Enter(r, st, json2.Array)
	if err != nil {
		return Node{}, i, err
	}

	var val Node

	for d.ForMore(r, &i, json2.Array, &err) {
		val, i, err = b.value(r, i, intern)
		if err != nil {
			return Node{}, i, err
		}

		b.tmp = append(b.tmp, val)
	}
	if err != nil {
		return Node{}, i, err
	}

	return b.Writer().Nodes(Array, b.tmp[mark:]).withSrc(st, i), i, nil
}

func (b *Buffer) object(r []byte, st int, intern bool) (Node, int, error) {
	mark := len(b.tmp)
	defer func() { b.tmp = b.tmp[:mark] }()

	var d json2.Iterator

	i, err := d.Enter(r, st, json2.Object)
	if err != nil {
		return Node{}, i, err
	}

	var key, val Node

	for d.ForMore(r, &i, json2.Object, &err) {
		key, i, err = b.value(r, i, intern)
		if err != nil {
			return Node{}, i, err
		}

		val, i, err = b.value(r, i, intern)
		if err != nil {
			return Node{}, i, err
		}

		b.tmp = append(b.tmp, key, val)
	}
	if err != nil {
		return Node{}, i, err
	}

	return b.Writer().Nodes(Object, b.tmp[mark:]).withSrc(st, i), i, nil
}

func (b BufferWriter) FromJSON(r []byte) (Node, error) {
	return b.Buffer.valueFull(r, true)
}

func (b BufferWriter) DecodeJSON(r []byte, st int) (Node, int, error) {
	return b.value(r, st, true)
}

func (b BufferWriter) Span(op Opcode, s []byte) Node {
	off := len(b.src) + len(b.text)
	b.text = append(b.text, s...)

	return makeNode(op.Op(), off, len(s))
}

func (b BufferWriter) Bytes(s []byte) Node {
	return b.Span(String, s)
}

func (b BufferWriter) String(s string) Node {
	return b.Bytes([]byte(s))
}

func (b BufferWriter) Int(x int) Node {
	return MakeInt(int64(x))
}

func (b BufferWriter) Int64(x int64) Node {
	return MakeInt(x)
}

func (b BufferWriter) Float(x float64) Node {
	return MakeFlt(x)
}

func (b BufferWriter) Bool(x bool) Node {
	if x {
		return Node{op: True}
	}

	return Node{op: False}
}

func (b BufferWriter) Null() Node {
	return Node{op: Null}
}

// Nodes assembles nodes into a fresh container of kind cont (Array or Object)
// in b. The container's own source span, if it has one, is set with withSrc by
// the caller that read it.
func (b BufferWriter) Nodes(cont Opcode, nodes []Node) Node {
	off := len(b.code)
	b.code = append(b.code, nodes...)

	n := len(nodes)
	if cont.Op() == Object {
		n /= 2
	}

	return makeNode(cont.Op(), off, n)
}

// Array assembles elems into a fresh array value in b.
func (b BufferWriter) Array(elems ...Node) Node { return b.Nodes(Array, elems) }

// Object assembles alternating key/value words into a fresh object value in b.
func (b BufferWriter) Object(kv ...Node) Node { return b.Nodes(Object, kv) }

func (b BufferWriter) CopyFrom(src BufferReader, op Node) Node {
	switch op.Op() {
	case Null, True, False, IntLit, FltLit:
		return op // self-contained words: the value rides the opcode, no bytes to copy
	case Number, String:
		return b.Span(op.Op(), src.Span(op))
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
		panic(op.Op())
	}
}

// AppendPointer writes the data path the steps descended, in jq syntax:
// .users[0].name, and "." at the root. Steps the data did not follow — $ref,
// allOf, if — contribute nothing, which is what makes them None.
func (b BufferReader) AppendPointer(w []byte, steps []Step) []byte {
	mark := len(w)

	for _, st := range steps {
		switch st.DataKey.Op() {
		case String:
			key := b.Span(st.DataKey)

			if identKey(key) {
				w = append(append(w, '.'), key...)
				continue
			}

			var e json2.Emitter

			w = e.AppendString(append(w, '.'), key)
		case IntLit:
			w = append(strconv.AppendInt(append(w, '['), st.DataKey.Int(), 10), ']')
		}
	}

	if len(w) == mark {
		w = append(w, '.')
	}

	return w
}

// identKey reports whether jq would take the key bare after a dot.
func identKey(s []byte) bool {
	for i, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c == '_':
		case i > 0 && c >= '0' && c <= '9':
		default:
			return false
		}
	}

	return len(s) != 0
}

func (b BufferReader) AppendJSON(w []byte, val Node) []byte {
	switch val.Op() {
	case Null:
		return append(w, "null"...)
	case True:
		return append(w, "true"...)
	case False:
		return append(w, "false"...)
	case Number:
		return append(w, b.Span(val)...)
	case String:
		var e json2.Emitter

		return e.AppendString(w, b.Span(val))
	case IntLit:
		return strconv.AppendInt(w, val.Int(), 10)
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
		panic(val.Op())
	}
}

// Span is the node's bytes, read from the input or written into the text tail —
// the two are one address space here, so the caller need not care which. A
// string kind holds the string itself, decoded; a Number holds its lexeme. The
// result is empty for a node whose kind carries bytes but this one has none: a
// synthesized container, or a bare null/true/false word. It panics on a kind
// that never carries any (IntLit, FltLit, Node{}) — that is a property of the
// opcode, not of the value, so it cannot depend on where the node came from.
// Ask Source when the origin is what you need.
func (b BufferReader) Span(op Node) []byte {
	off, end := b.span(op)

	// Spans resolve a virtual src++text concat: bytes below len(src) live in the
	// read-only input, the rest in the produced-scalar tail.
	if off < len(b.src) {
		return b.src[off:end]
	}

	return b.text[off-len(b.src) : end-len(b.src)]
}

func (b BufferReader) span(op Node) (off, end int) {
	switch op.Op() {
	case Number, String, Pattern, ID, Ref, Null, False, True:
		return op.SpanInt()
	case None:
		return 0, 0
	case Object, Array:
		off, end, _ = op.Src()
		return off, end
	default:
		panic(op.Op())
	}
}

// Nodes unwraps a container node into its child words. Result slice is owned by
// Buffer. Panics on a node that is not a container (single-child pointers like
// Not/Items/Const are references, not containers).
func (b BufferReader) Nodes(op Node) []Node {
	off, n := op.OffInt(), op.ArgInt()

	switch op.Op() {
	case Object, Properties, PatternProps, Defs, Raw, Ext:
		n *= 2 // pair-blocks: key + subschema per entry
	case All, AllOf, AnyOf, OneOf, Prefix, Enum, Required, Array:
		// list-blocks: one child per entry
	default:
		panic(op.Op())
	}

	return b.code[off : off+n]
}

func (b BufferReader) NodesLen(op Node) int {
	return op.ArgInt()
}

func (b BufferReader) NodesAt(op Node, i int) (k, v Node) {
	off, n := op.OffInt(), op.ArgInt()
	if i < 0 {
		i = n + i
	}
	if i >= n || i < 0 {
		return Node{}, Node{}
	}

	switch op.Op() {
	case Object, Properties, PatternProps, Defs, Raw, Ext:
		i *= 2 // kv-pairs

		return b.code[off+i], b.code[off+i+1]
	case All, AllOf, AnyOf, OneOf, Prefix, Enum, Required, Array:
		return MakeInt(int64(i)), b.code[off+i]
	default:
		panic(op.Op())
	}
}

// Ext returns the value node of the extension keyword named key (e.g. "x-type")
// among the keywords of schema node op (an All), or Node{} if absent. The key is
// matched whole, so any extension prefix falls into the same path. The value is
// left to the caller to interpret; Node{} is never a valid value.
func (b BufferReader) Ext(op Node, key string) Node {
	return b.named(op, Ext, key)
}

// Raw returns the value node of the keyword named key kept verbatim among the
// keywords of schema node op (an All), or Node{} if absent — an annotation the
// vocabulary carries but never applies ("title", "format", "$comment"), or a
// keyword outside it. An extension keyword is an Ext, not a Raw.
func (b BufferReader) Raw(op Node, key string) Node {
	return b.named(op, Raw, key)
}

// named finds the pair keyword of kind kind spelled key. Ext and Raw repeat
// within an All, so unlike every other keyword they are reached by name.
func (b BufferReader) named(op Node, kind Opcode, key string) Node {
	if op.Op() != All {
		panic(op.Op())
	}

	for _, ch := range b.Nodes(op) {
		if ch.Op() != kind {
			continue
		}

		k, v := b.NodesAt(ch, 0)
		if string(b.Span(k)) == key {
			return v
		}
	}

	return Node{}
}

// Find is the value under key in a pair-block: a data Object, or a schema
// Properties, PatternProps or Defs. Node{} when the key is not there — a value
// node is never Node{}, so the answer is unambiguous. Keys compare as bytes
// because a string node holds the string, not its spelling.
func (b BufferReader) Find(op Node, key string) Node {
	switch op.Op() {
	case Object, Properties, PatternProps, Defs:
	default:
		panic(op.Op())
	}

	off := op.OffInt()

	for i := range op.ArgInt() {
		if string(b.Span(b.code[off+2*i])) == key {
			return b.code[off+2*i+1]
		}
	}

	return Node{}
}

// Iter ranges over the children of any node, pairing key with value — the
// generalization of Nodes/NodesAt (pair- and list-blocks), Deref (single-child
// pointers), and the variadic Additional. Pair-blocks yield (key, sub);
// list-blocks yield (IntLit index, elem); single-child nodes yield (Node{}, sub). A
// scalar or in-opcode keyword (Type, MinLen, Pattern, …) has no children.
func (b BufferReader) Iter(op Node) iter.Seq2[Node, Node] {
	off := op.OffInt()

	return func(yield func(k, v Node) bool) {
		switch op.Op() {
		case Object, Properties, PatternProps, Defs, Raw, Ext:
			for i := range op.ArgInt() {
				if !yield(b.code[off+2*i], b.code[off+2*i+1]) {
					return
				}
			}
		case All, AllOf, AnyOf, OneOf, Prefix, Enum, Required, Array:
			for i := range op.ArgInt() {
				if !yield(MakeInt(int64(i)), b.code[off+i]) {
					return
				}
			}
		case Additional:
			if op.ArgInt() == 3 {
				yield(Node{}, b.code[off+2]) // props, patterns, sub — sub only
				return
			}

			yield(Node{}, b.code[off])
		case Not, Items, If, Then, Else, Const, Default, Minimum, Maximum, ExclMin, ExclMax, MultipleOf:
			yield(Node{}, b.code[off])
		}
	}
}

// Keyword returns the keyword node of kind want among the keywords of schema
// node op (an All), or Node{} if absent. Every keyword is unique per schema, so the
// match is unambiguous — except Ext and Raw, which repeat and are keyed by name;
// look those up with Ext or Raw. Read the returned node with Deref, Nodes, or
// its Imm, per the keyword.
func (b BufferReader) Keyword(op Node, want Opcode) Node {
	if op.Op() != All {
		panic(op.Op())
	}

	for _, ch := range b.Nodes(op) {
		if ch.Op() == want.Op() {
			return ch
		}
	}

	return Node{}
}

// Deref returns the single child of a pointer node — the subschema or operand it
// points at (Not/Items/Const/Default/Minimum/…). These hold one reference, not a
// list, so it panics on any other op. Additional is variadic — split it with
// PropertiesParts instead.
func (b BufferReader) Deref(op Node) Node {
	switch op.Op() {
	case Not, Items, If, Then, Else, Const, Default, Minimum, Maximum, ExclMin, ExclMax, MultipleOf:
		return b.code[op.OffInt()]
	default:
		panic(op.Op())
	}
}

// String is the string a String, Pattern, ID or Ref node holds. Strings are
// stored decoded, so this is the node's own bytes: no copy, no scratch, valid
// for as long as the buffer is not rewritten.
func (b BufferReader) String(op Node) []byte {
	switch op.Op() {
	case String, Pattern, ID, Ref:
		return b.Span(op)
	default:
		panic(op.Op())
	}
}

// Int, Int64, and Float read a numeric value node, whether it was decoded from
// JSON text (a Num span) or synthesized in-opcode (an IntLit or FltLit). A
// non-numeric node yields ErrNotNumber; a malformed Num span yields the decoder's
// parse error.
func (b BufferReader) Int(op Node) (int, error) {
	v, err := b.Int64(op)
	return int(v), err
}

func (b BufferReader) Int64(op Node) (int64, error) {
	switch op.Op() {
	case IntLit:
		return op.Int(), nil
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

func (b BufferReader) Float(op Node) (float64, error) {
	switch op.Op() {
	case FltLit:
		return op.Flt(), nil
	case IntLit:
		return float64(op.Int()), nil
	case Number:
		return json2.Value(b.Span(op)).Float64()
	default:
		return 0, ErrNotNumber
	}
}
