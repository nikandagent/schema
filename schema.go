package schema

import "regexp"

type (
	Schema struct {
		Flags Flags // canonicalization switches; the zero value is the canonical default

		root Opcode
		prog Buffer // compiled program: code nodes + schema bytes (src) + compile scratch (tmp)

		defs   []def
		xhooks []hook // name -> "x-name" hook, bound at compile (SetXHook)

		id   string             // this document's base URI ($id or registration key)
		docs map[string]*Schema // shared registry: base URI -> compiled document

		// Resolve loads a document not already registered, on first $ref to it.
		// base is the referrer's $id, ref the opaque handle (left of '#'). The
		// caller owns all path/version/transport logic.
		Resolve func(base, ref string) ([]byte, error)

		patterns map[Opcode]*regexp.Regexp // pattern node -> compiled regex, filled at compile

		b Buffer // reused data arena for Validate/Rewrite
	}

	// Opcode is a schema instruction.
	Opcode uint64

	// Flags select deviations from the canonical default. The zero value
	// canonicalizes both schema and data and fills defaults; the Keep* bits opt
	// out of a step, RejectUnknown opts in.
	Flags uint32
)

const (
	SchemaKeepOrder     Flags = 1 << iota // keep authored keyword & required order, don't canonicalize
	SchemaRejectUnknown                   // reject unknown keywords instead of keeping them (spec keeps)
	KeepKeyOrder                          // keep input object-key order, don't reorder to properties
	KeepMissing                           // keep missing properties absent, don't fill defaults
)

// DataPreserve rewrites data without changing its content: no reordering and no
// inserted defaults. Whitespace is still normalized.
const DataPreserve = KeepKeyOrder | KeepMissing

func (f Flags) Is(g Flags) bool { return f&g == g }
func (f *Flags) Set(g Flags)    { *f |= g }
func (f *Flags) Unset(g Flags)  { *f &^= g }

// word: payload:56 | shape:3 | code:5
//
// payload by shape:
//
//	imm                value:56
//	span      off:32 |   len:24
//	block   index:32 | count:24
//	ref    target:32 |   arg:24
const (
	shapeShift = 5
	opMask     = 1<<8 - 1

	argShift = 8
	offShift = 32

	maxArg = 1<<24 - 1
	maxOff = 1<<32 - 1
	maxImm = 1<<56 - 1
)

const (
	imm Opcode = iota << shapeShift
	span
	block
	ref
)

// imm
const (
	Pass Opcode = imm | iota
	bad
	Fail
	Null
	False
	True
	Type
	Unique
	MinLen
	MaxLen
	MinItems
	MaxItems
	MinProps
	MaxProps
	Canon
)

// span
const (
	Num Opcode = span | iota
	Str
	Pattern
)

// block
const (
	And Opcode = block | iota
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
)

// ref
const (
	Ref Opcode = ref | iota
	CallExt
)
