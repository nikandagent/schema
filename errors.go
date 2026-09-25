package schema

import (
	"errors"
	"fmt"

	"nikand.dev/go/json2"
)

type (
	// Diag is one finding: Code says what went wrong, Op is the schema node it
	// is about — the keyword, or the required entry — and Val the offending
	// value. Read either through its own arena for the details (limit, types,
	// name, the value itself) and Src for where it sits in the text. Steps is
	// the descent to the value, copied from Applier.Steps under SaveSteps, nil
	// otherwise; its data keys live in the walk's data arena, so render it
	// through the Applier that walked:
	// a.Buffer.Reader().AppendPointer(w, d.Steps).
	Diag struct {
		Code  DiagCode
		Op    Node
		Val   Node
		Steps []Step
	}

	// DiagCode classifies a validation failure. The app switches on it and
	// renders its own text; String gives the built-in default message.
	DiagCode int

	// Diagnostics is a list of findings as an error. Validate returns findings as
	// values next to a nil error; wrap them in Diagnostics to propagate them, and
	// recover them higher up with AsDiag. Compile and the options return a
	// one-element Diagnostics. errors.Is matches the category of the first
	// finding (ErrKeyword, ErrRef, ...).
	Diagnostics []Diag
)

const spaces = "                                                                                                                                "

const (
	TypeMismatch DiagCode = iota + 1
	TooShort
	TooLong
	BelowMinimum
	AboveMaximum
	BelowMinimumExcl
	AboveMaximumExcl
	NotMultipleOf
	TooFewItems
	TooManyItems
	DuplicateItems
	TooFewProps
	TooManyProps
	MissingRequired
	MustMatchEnum
	MustConst
	PatternMismatch
	FormatMismatch
	MustNotMatch
	MustMatchAny
	MustMatchOne
	MustMatchOnlyOne
	Forbidden

	// compile-time codes
	SchemaMustBeObject
	InvalidTypeShape
	UnknownType
	MustBeObject
	MustBeArray
	RequiredNotString
	MustBeNumber
	MustBeInteger
	MustBeBool
	MustBeString
	EmptyRef
	DuplicateAnchor
	DuplicateID
	UnresolvedRef
	NoResolver
	BadPattern
	UnknownKeyword
	UnsupportedKeyword
	UnsupportedFormat
)

// UserDiagBase is the first DiagCode reserved for application use. A Walk handler
// defines its own codes as UserDiagBase + n (n >= 0); the library never assigns or
// renders these, so String returns "" and the app formats them itself.
const UserDiagBase DiagCode = 1 << 16

var diagText = [...]string{
	TypeMismatch:     "wrong type",
	TooShort:         "too short",
	TooLong:          "too long",
	BelowMinimum:     "less than minimum",
	AboveMaximum:     "greater than maximum",
	BelowMinimumExcl: "not above exclusive minimum",
	AboveMaximumExcl: "not below exclusive maximum",
	NotMultipleOf:    "not a multiple",
	TooFewItems:      "too few items",
	TooManyItems:     "too many items",
	DuplicateItems:   "duplicate items",
	TooFewProps:      "too few properties",
	TooManyProps:     "too many properties",
	MissingRequired:  "missing required property",
	MustMatchEnum:    "not in enum",
	MustConst:        "not the const value",
	PatternMismatch:  "does not match pattern",
	FormatMismatch:   "does not match format",
	MustNotMatch:     "matches a forbidden schema",
	MustMatchAny:     "matches none of the schemas",
	MustMatchOne:     "matches none of the schemas",
	MustMatchOnlyOne: "matches more than one schema",
	Forbidden:        "schema forbids any value",

	SchemaMustBeObject: "a schema must be an object or a boolean",
	InvalidTypeShape:   `"type" must be a string or array of type names`,
	UnknownType:        `"type" contains an unknown type name`,
	MustBeObject:       "must be an object",
	MustBeArray:        "must be an array",
	RequiredNotString:  `"required" entries must be strings`,
	MustBeNumber:       "must be a number",
	MustBeInteger:      "must be an integer",
	MustBeBool:         "must be a boolean",
	MustBeString:       "must be a string",
	EmptyRef:           `"$ref" must not be empty`,
	DuplicateAnchor:    "duplicate $anchor",
	DuplicateID:        "duplicate $id",
	UnresolvedRef:      "not found",
	NoResolver:         "no resolver",
	BadPattern:         "invalid regular expression",
	UnknownKeyword:     "unknown keyword",
	UnsupportedKeyword: "unsupported keyword",
	UnsupportedFormat:  "unsupported format",
}

func (c DiagCode) String() string {
	if int(c) < 0 || int(c) >= len(diagText) {
		return ""
	}

	return diagText[c]
}

// JSON-shape errors, reused from the decoder.
var (
	ErrSyntax       = json2.ErrSyntax
	ErrTrailingData = json2.ErrTrailingData
)

// ErrNotNumber is returned by the numeric readers (Int, Int64, Float) when the
// value node is not a number; ErrNotInteger when an integer reader is handed a
// number with a fractional part.
var (
	ErrNotNumber  = errors.New("not a number")
	ErrNotInteger = errors.New("not an integer")
)

// Error categories: what a Diagnostics error is, by the code of its first
// finding; errors.Is(err, ErrKeyword) classifies it.
var (
	ErrInvalid        = errors.New("invalid document")
	ErrKeyword        = errors.New("invalid keyword value")
	ErrUnknownKeyword = errors.New("unknown keyword")
	ErrUnsupported    = errors.New("unsupported keyword")
	ErrPattern        = errors.New("invalid pattern")
	ErrRef            = errors.New("unresolved ref")
)

// Category is the error a code classifies as.
func (c DiagCode) Category() error {
	switch c {
	case SchemaMustBeObject, InvalidTypeShape, UnknownType, MustBeObject, MustBeArray, RequiredNotString,
		MustBeNumber, MustBeInteger, MustBeBool, MustBeString, EmptyRef:
		return ErrKeyword
	case DuplicateAnchor, DuplicateID, UnresolvedRef, NoResolver:
		return ErrRef
	case BadPattern:
		return ErrPattern
	case UnknownKeyword:
		return ErrUnknownKeyword
	case UnsupportedKeyword, UnsupportedFormat:
		return ErrUnsupported
	default:
		return ErrInvalid
	}
}

// FormatNicely appends the snippet(s) with a default context width.
func (d Diag) FormatNicely(w, src []byte) []byte        { return d.FormatNicelyContext(w, src, 10, 10) }
func (e Diagnostics) FormatNicely(w, src []byte) []byte { return e.FormatNicelyContext(w, src, 10, 10) }

// FormatNicelyContext appends a two-line snippet locating the diagnostic in src:
// the offending span with up to before/after context bytes around it (elided
// with "..."), then a caret under the span and the capitalized message.
//
//	..._up to before here_}_up to after here...
//	                      ^ Message here
func (d Diag) FormatNicelyContext(w, src []byte, before, after int) []byte {
	off, end := d.span()
	off, end = clampSpan(off, end, len(src))

	// An "..." is 3 chars, so eliding 3 or fewer saves nothing — show the source.
	start := max(off-before, 0)
	if start <= 3 {
		start = 0
	}

	stop := min(end+after, len(src))
	if len(src)-stop <= 3 {
		stop = len(src)
	}

	lead := start > 0 // more source precedes/follows the window
	trail := stop < len(src)

	if lead {
		w = append(w, "..."...)
	}

	// Flatten control whitespace to spaces so the caret column stays aligned.
	for _, c := range src[start:stop] {
		if c == '\n' || c == '\r' || c == '\t' {
			c = ' '
		}

		w = append(w, c)
	}

	if trail {
		w = append(w, "..."...)
	}

	w = append(w, '\n')

	pad := off - start
	if lead {
		pad += 3 // under the leading "..."
	}

	for pad > len(spaces) {
		w = append(w, spaces...)
		pad -= len(spaces)
	}

	w = append(w, spaces[:pad]...)
	w = append(w, '^', ' ')
	w = appendCapitalized(w, d.Code.String())

	return append(w, '\n')
}

// appendCapitalized appends s with its first ASCII letter upper-cased.
func appendCapitalized(w []byte, s string) []byte {
	if s == "" {
		return w
	}

	c := s[0]
	if c >= 'a' && c <= 'z' {
		c -= 'a' - 'A'
	}

	return append(append(w, c), s[1:]...)
}

func clampSpan(off, end, n int) (int, int) {
	off = min(max(off, 0), n)
	end = min(max(end, off), n)

	return off, end
}

func (e Diagnostics) Error() string {
	switch len(e) {
	case 0:
		return ErrInvalid.Error()
	case 1:
		return fmt.Sprintf("%v: %v", e[0].Code.Category(), e[0].Code)
	default:
		return fmt.Sprintf("%v: %v (+%d more)", e[0].Code.Category(), e[0].Code, len(e)-1)
	}
}

func (e Diagnostics) Is(target error) bool {
	return len(e) != 0 && e[0].Code.Category() == target
}

// FormatNicelyContext appends each diagnostic's snippet (see
// Diag.FormatNicelyContext), separated by a blank line.
func (e Diagnostics) FormatNicelyContext(w, src []byte, before, after int) []byte {
	for i, d := range e {
		if i != 0 {
			w = append(w, '\n')
		}

		w = d.FormatNicelyContext(w, src, before, after)
	}

	return w
}

// AsError wraps diags as Diagnostics, or returns a nil error when there are none,
// so propagating a validation result stays a one-liner.
func AsError(diags []Diag) error {
	if len(diags) == 0 {
		return nil
	}

	return Diagnostics(diags)
}

// AsDiag returns the diagnostics carried by err, or nil when it carries none.
func AsDiag(err error) []Diag {
	var inv Diagnostics
	if errors.As(err, &inv) {
		return inv
	}

	return nil
}

// normSyntax maps json2's short-buffer signal to ErrSyntax. json2 is a streaming
// decoder, so a truncated but not-yet-invalid prefix (e.g. "{") reads as "need
// more data"; for a complete document that is simply malformed input.
func normSyntax(err error) error {
	if errors.Is(err, json2.ErrShortBuffer) {
		return ErrSyntax
	}

	return err
}

// span is where a rendering points: the offending value, or the keyword itself
// when there is no value — a schema that would not compile. Which text that is
// depends on which node answered, so nothing but FormatNicely uses it; a caller
// asks the node it means, d.Val.Src or d.Op.Src.
func (d Diag) span() (off, end int) {
	if off, end, ok := d.Val.Src(); ok {
		return off, end
	}

	off, end, _ = d.Op.Src()

	return off, end
}

// kerr is a schema-side failure about a keyword that has no node yet: the kind
// is all we can say about it here. locate stamps the place on the way out, from
// the level that knows the whole "key": value pair.
func kerr(code DiagCode, kind Opcode) error {
	return serr(code, Node{op: kind})
}

// serr is a schema-side failure about op, which carries its own place in the
// schema text.
func serr(code DiagCode, op Node) error {
	return Diagnostics{{Code: code, Op: op}}
}
