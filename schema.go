package schema

import (
	"math"
	"regexp"
)

type (
	Schema struct {
		Flags Flags // canonicalization switches; the zero value is the canonical default

		root Opcode
		prog Buffer // compiled program: code nodes + schema bytes (src) + compile scratch (tmp)

		defs []def

		id   string             // this document's base URI ($id or registration key)
		docs map[string]*Schema // shared registry: base URI -> compiled document

		// Resolve loads a document not already registered, on first $ref to it.
		// base is the referrer's $id, ref the opaque handle (left of '#'). The
		// caller owns all path/version/transport logic.
		Resolve func(base, ref string) ([]byte, error)

		patterns map[Opcode]*regexp.Regexp // pattern node -> compiled regex, filled at compile

		c Applier

		defsbuf [3]def // absorbs the slack the Applier trim opens, keeping Schema near its 1536 bucket
	}

	// Opcode is a schema instruction.
	Opcode uint64

	// Flags select deviations from the canonical default. The zero value
	// canonicalizes both schema and data and fills defaults; the Keep* bits opt
	// out of a step, RejectUnknown opts in.
	Flags uint32
)

const (
	SchemaKeepOrder         Flags = 1 << iota // keep authored keyword & required order, don't canonicalize
	SchemaRejectUnknown                       // reject unknown keywords instead of keeping them (spec keeps)
	SchemaRejectUnsupported                   // reject recognized-but-unimplemented keywords (if, contains, ...)
	KeepKeyOrder                              // keep input object-key order, don't reorder to properties
	KeepMissing                               // keep missing properties absent, don't fill defaults
)

// DataPreserve rewrites data without changing its content: no reordering and no
// inserted defaults. Whitespace is still normalized.
const DataPreserve = KeepKeyOrder | KeepMissing

func (f Flags) Is(g Flags) bool { return f&g == g }
func (f *Flags) Set(g Flags)    { *f |= g }
func (f *Flags) Unset(g Flags)  { *f &^= g }

// Root is the compiled program's root node; walk it with SchemaBuf.
func (s *Schema) Root() Opcode { return s.root }

// SchemaBuf is the program arena (read-only): the nodes and bytes the schema
// keywords point into. Pair with Root to traverse the program.
func (s *Schema) SchemaBuf() BufferReader { return s.prog.Reader() }

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
	imm Opcode = iota << shapeShift
	span
	block

	span2 = span + 1<<(shapeShift-1) // second half of span
)

const (
	Pass Opcode = imm | iota
	Fail
	Type
	Unique
	MinLen
	MaxLen
	MinItems
	MaxItems
	MinProps
	MaxProps
	Canon

	None
	IntLit
	FltLit
	SrcOff
	SrcSpan
	Each

	bad // internal: unresolved ref/anchor target
)

const (
	Number Opcode = span | iota
	String
	Pattern

	Key
	Ref
)

const (
	Null Opcode = span2 | iota
	False
	True
)

const (
	All Opcode = block | iota
	AllOf
	AnyOf
	OneOf
	Enum
	Required
	Array
	Not
	Items
	Additional
	Const
	Default
	Minimum
	Maximum
	ExclMin
	ExclMax
	MultipleOf
	Object
	Properties
	PatternProps
	Defs
	Raw
	Ext // custom "x-" keyword: an inert Raw-like pair, acted on only in a Walk handler
)

func makeNode(op Opcode, off, n int) Opcode {
	if off < 0 || off > maxOff {
		panic(off)
	}
	if n < 0 || n > maxArg {
		panic(n)
	}

	return op | Opcode(n)<<argShift | Opcode(off)<<offShift
}

func MakeInt(v int64) Opcode {
	if v < minImm || v > maxImm {
		panic(v)
	}

	return IntLit | Opcode(v)<<argShift
}

func makeImm(op Opcode, v int) Opcode {
	if v < minImm || v > maxImm {
		panic(v)
	}

	return op | Opcode(v)<<argShift
}

// MakeFlt packs v into the opcode itself, tagged FltLit. The low 8 mantissa bits
// make room for the tag, so v is stored to ~44 mantissa bits (magnitude exact,
// ~13 significant digits). The +0x80 rounds to nearest instead of truncating,
// halving the error; the carry propagates correctly into the exponent.
func MakeFlt(v float64) Opcode {
	return FltLit | Opcode(math.Float64bits(v)+0x80)&^opMask
}

func (op Opcode) Op() Opcode   { return op & opMask }
func (op Opcode) Imm() int64   { return int64(op) >> argShift }
func (op Opcode) Arg() int64   { return int64(op >> argShift & argMask) }
func (op Opcode) Off() int64   { return int64(op >> offShift & offMask) }
func (op Opcode) Flt() float64 { return math.Float64frombits(uint64(op &^ opMask)) }

// OffInt, ArgInt, and ImmInt narrow the accessors to int for indexing and
// lengths; the payload fields are far below math.MaxInt on any real program.
func (op Opcode) OffInt() int { return int(op.Off()) }
func (op Opcode) ArgInt() int { return int(op.Arg()) }
func (op Opcode) ImmInt() int { return int(op.Imm()) }

func (op Opcode) SpanInt() (off, end int) { off = op.OffInt(); return off, off + op.ArgInt() }
