package schema

type (
	Schema struct {
		root Opcode
		code []Opcode

		schema []byte

		tmp []Opcode
	}

	// Opcode is a schema instruction.
	Opcode uint64
)

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
	shapeMask  = 7 << shapeShift
	codeMask   = 1<<shapeShift - 1

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
)

// ref
const (
	Ref Opcode = ref | iota
	CallExt
)
