package schema

import (
	"math"
	"regexp"
)

type (
	Schema struct {
		Flags Flags // canonicalization switches; the zero value is the canonical default

		root Node
		prog Buffer // compiled program: code nodes + schema bytes (src) + compile scratch (tmp)

		defs []def

		// ID is the document's base URI: where it was retrieved from, or the
		// handle it is registered under. Set it before Compile for a document
		// whose text does not name itself — a top-level $id replaces it.
		ID string

		docs map[string]*Schema // shared registry: base URI -> compiled document

		// Resolve loads a document not already registered, on first $ref to it.
		// base is the referrer's $id, ref the opaque handle (left of '#'). The
		// caller owns all path/version/transport logic.
		Resolve func(base, ref string) ([]byte, error)

		patterns map[Node]*regexp.Regexp // pattern node -> compiled regex, filled at compile

		defsbuf [9]def // inline room for a small $defs table, filling the 1280 bucket
	}

	// Opcode is a schema instruction: the kind, plus the payload packed into
	// the same word.
	Opcode uint64

	// Node is one instruction as it is stored: the opcode and a meta word.
	// meta mirrors the opcode layout, so its off and len fields read with the
	// same accessors; what it holds is fixed by the opcode:
	//
	//	IntLit         the int64 value, exact
	//	FltLit         the float64 bits, exact
	//	anything else  the node's span in the source text, zero when it has none
	//
	// The low 8 bits of meta — the opcode's own code and shape slot — are free
	// for flags.
	Node struct {
		op   Opcode
		meta Opcode
	}

	// Flags select deviations from the canonical default. The zero value
	// canonicalizes both schema and data and fills defaults; the Keep* bits opt
	// out of a step, RejectUnknown opts in.
	Flags uint32

	// Types is a set of JSON types: the types a Type keyword admits, or the
	// single type a value has. The empty set is a schema node with no type
	// keyword, so an absent keyword needs no special case.
	Types uint8
)

const (
	SchemaKeepOrder         Flags = 1 << iota // keep authored keyword & required order, don't canonicalize
	SchemaRejectUnknown                       // reject unknown keywords instead of keeping them (spec keeps)
	SchemaRejectUnsupported                   // reject recognized-but-unimplemented keywords (if, contains, ...)
	KeepKeyOrder                              // keep input object-key order, don't reorder to properties
	KeepMissing                               // keep missing properties absent, don't fill defaults
	AssertStringFormat                        // check "format" instead of carrying it as an annotation
	AssertEmailUseful                         // judge "email" as people write them: no quoted local part, no address literal
	SaveSteps                                 // copy the descent into each Diag.Steps
)

// DataPreserve rewrites data without changing its content: no reordering and no
// inserted defaults. Whitespace is still normalized.
const DataPreserve = KeepKeyOrder | KeepMissing

func (f Flags) Is(g Flags) bool { return f&g == g }
func (f *Flags) Set(g Flags)    { *f |= g }
func (f *Flags) Unset(g Flags)  { *f &^= g }

// Root is the compiled program's root node; walk it with SchemaBuf.
func (s *Schema) Root() Node { return s.root }

// Reader is the program arena (read-only): the nodes and bytes the schema
// keywords point into. Pair with Root to traverse the program.
func (s *Schema) Reader() BufferReader { return s.prog.Reader() }

// word: payload:56 | shape:3 | code:5
//
// payload by shape:
//
//	imm              value:56
//	span    off:32 |   len:24
//	block index:32 | count:24
//
// span2 is the top half of the span code range (code >= 16): valueless scalars
// (null/true/false) that still carry a source span. Ref is span-shaped too, in
// the low span codes.
const (
	shapeShift = 5
	opMask     = 1<<8 - 1

	argShift = 8
	offShift = 32

	argMask = 1<<24 - 1 // full fields, for extraction
	offMask = 1<<32 - 1
	immMask = 1<<56 - 1

	// The top 4 values of each field are reserved as future sentinels (e.g. a
	// field == its mask-k means the real value continues in the next opcode).
	maxArg = argMask - 4
	maxOff = offMask - 4
	maxImm = immMask>>1 - 4
	minImm = -(immMask>>1 + 1)
)

const (
	scalar = iota << shapeShift
	span
	imm
	ref
	block
)

const (
	None Opcode = scalar | iota
	Pass
	Fail
	Null
	False
	True
)

const (
	Number Opcode = span | iota
	String
	Pattern

	ID

	Ref
)

const (
	Type Opcode = imm | iota
	Unique
	MinLen
	MaxLen
	MinItems
	MaxItems
	MinProps
	MaxProps
	Format

	IntLit
	FltLit
)

const (
	Not Opcode = ref | iota
	Const
	Default

	Raw
	Ext // custom "x-" keyword: an inert Raw-like pair, acted on only in a Walk handler

	If
	Then
	Else
)

const (
	All Opcode = block | iota
	AllOf
	AnyOf
	OneOf
	Enum
	Required
	Prefix
	Items
	Additional
	Minimum
	Maximum
	ExclMin
	ExclMax
	MultipleOf
	Properties
	PatternProps
	Defs

	Array
	Object
)

const (
	TypeNull Types = 1 << iota
	TypeBoolean
	TypeInteger
	TypeNumber
	TypeString
	TypeArray
	TypeObject

	typeErr // an unknown type name; rejected at compile, never in a program
)

var typeNames = []struct {
	bit  Types
	name string
}{
	{TypeNull, "null"},
	{TypeBoolean, "boolean"},
	{TypeInteger, "integer"},
	{TypeNumber, "number"},
	{TypeString, "string"},
	{TypeArray, "array"},
	{TypeObject, "object"},
}

// TypesOf is the set a Type keyword admits, or the empty set for None — so the
// absent keyword Keyword returns needs no check. Any other node panics.
func TypesOf(op Node) Types {
	switch op.Op() {
	case Type:
		return Types(op.Imm())
	case None:
		return 0
	default:
		panic(op.Op())
	}
}

func (t Types) Is(g Types) bool  { return t&g == g }
func (t Types) Any(g Types) bool { return t&g != 0 }

func (t Types) String() string {
	var b []byte

	for _, n := range typeNames {
		if !t.Any(n.bit) {
			continue
		}

		if b != nil {
			b = append(b, '|')
		}

		b = append(b, n.name...)
	}

	if b == nil {
		return "empty"
	}

	return string(b)
}

func pack(op Opcode, off, n int) Opcode {
	if off < 0 || int64(off) > maxOff {
		panic(off)
	}
	if n < 0 || n > maxArg {
		panic(n)
	}

	return op | Opcode(n)<<argShift | Opcode(off)<<offShift
}

func makeNode(op Opcode, off, n int) Node {
	return Node{op: pack(op, off, n)}
}

func makeImm(op Opcode, v int) Node {
	if int64(v) < minImm || int64(v) > maxImm {
		panic(v)
	}

	return Node{op: op | Opcode(v)<<argShift}
}

// MakeInt and MakeFlt carry the value in the meta word, so a literal is the
// number the caller wrote, to the last bit.
func MakeInt(v int64) Node {
	return Node{op: IntLit, meta: Opcode(v)}
}

func MakeFlt(v float64) Node {
	return Node{op: FltLit, meta: Opcode(math.Float64bits(v))}
}

func (op Opcode) Op() Opcode { return op & opMask }
func (op Opcode) Imm() int64 { return int64(op) >> argShift }
func (op Opcode) Arg() int64 { return int64(op >> argShift & argMask) }
func (op Opcode) Off() int64 { return int64(op >> offShift & offMask) }

// OffInt, ArgInt, and ImmInt narrow the accessors to int for indexing and
// lengths; the payload fields are far below math.MaxInt on any real program.
func (op Opcode) OffInt() int { return int(op.Off()) }
func (op Opcode) ArgInt() int { return int(op.Arg()) }
func (op Opcode) ImmInt() int { return int(op.Imm()) }

func (op Opcode) SpanInt() (off, end int) { off = op.OffInt(); return off, off + op.ArgInt() }

// Op is the node's opcode, the kind to switch on. Imm, Arg, Off and the Int
// variants read the opcode payload; Int and Flt read a literal's value.
func (n Node) Op() Opcode   { return n.op.Op() }
func (n Node) Imm() int64   { return n.op.Imm() }
func (n Node) Arg() int64   { return n.op.Arg() }
func (n Node) Off() int64   { return n.op.Off() }
func (n Node) Int() int64   { return int64(n.meta) }
func (n Node) Flt() float64 { return math.Float64frombits(uint64(n.meta)) }

func (n Node) OffInt() int { return n.op.OffInt() }
func (n Node) ArgInt() int { return n.op.ArgInt() }
func (n Node) ImmInt() int { return n.op.ImmInt() }

func (n Node) SpanInt() (off, end int) { return n.op.SpanInt() }

// IsNone reports whether n is nothing: the zero Node, which is what a lookup
// that found no node returns. The zero value is the sentinel, so Node{} is how
// you write one.
func (n Node) IsNone() bool { return n.op.Op() == None }

// Src is the node's span in the text it was parsed from: src[off:end] is the
// JSON that produced it, quotes and all. ok is false for a node that never was
// text — one a Writer synthesized, or a literal, which spends meta on its value.
func (n Node) Src() (off, end int, ok bool) {
	switch n.op.Op() {
	case IntLit, FltLit, None:
		return 0, 0, false
	}

	if n.meta == 0 {
		return 0, 0, false
	}

	off, end = n.meta.SpanInt()

	return off, end, true
}

// withSrc records where n was read from. A zero-length span is dropped: meta
// zero is what "no source" means, and no real token is empty.
func (n Node) withSrc(off, end int) Node {
	if end <= off {
		return n
	}

	n.meta = pack(0, off, end-off)

	return n
}
