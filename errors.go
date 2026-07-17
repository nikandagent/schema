package schema

import (
	"errors"
	"fmt"

	"nikand.dev/go/json2"
)

type (
	Error struct {
		Diag Diag
		Err  error
	}

	Diag struct {
		Code     DiagCode
		Op       Opcode
		Off, End int
	}

	// DiagCode classifies a validation failure. The app switches on it and
	// renders its own text; String gives the built-in default message.
	DiagCode int

	// Diagnostics carries validation diagnostics as an error, so a caller can return
	// them through a plain error result and recover them higher up the stack with
	// errors.As(err, &inv). Diagnostics are not errors on their own — Validate
	// returns them alongside a nil error; wrap them in Diagnostics only to propagate.
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
	MustNotMatch
	MustMatchAny
	MustMatchOne
	Forbidden
	InvalidObjectKey
	InvalidArrayIndex

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
	UnresolvedRef
	NoResolver
	BadPattern
	UnknownKeyword
	UnsupportedKeyword
	InvalidAtKey
)

// UserDiagBase is the first DiagCode reserved for application use. A Walk handler
// defines its own codes as UserDiagBase + n (n >= 0); the library never assigns or
// renders these, so String returns "" and the app formats them itself.
const UserDiagBase DiagCode = 1 << 16

var diagText = [...]string{
	TypeMismatch:      "wrong type",
	TooShort:          "too short",
	TooLong:           "too long",
	BelowMinimum:      "less than minimum",
	AboveMaximum:      "greater than maximum",
	BelowMinimumExcl:  "not above exclusive minimum",
	AboveMaximumExcl:  "not below exclusive maximum",
	NotMultipleOf:     "not a multiple",
	TooFewItems:       "too few items",
	TooManyItems:      "too many items",
	DuplicateItems:    "duplicate items",
	TooFewProps:       "too few properties",
	TooManyProps:      "too many properties",
	MissingRequired:   "missing required property",
	MustMatchEnum:     "not in enum",
	MustConst:         "not the const value",
	PatternMismatch:   "does not match pattern",
	MustNotMatch:      "matches a forbidden schema",
	MustMatchAny:      "matches none of the schemas",
	MustMatchOne:      "must match exactly one schema",
	Forbidden:         "schema forbids any value",
	InvalidObjectKey:  "object indexed as array",
	InvalidArrayIndex: "array keyed as object",

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
	UnresolvedRef:      "not found",
	NoResolver:         "no resolver",
	BadPattern:         "invalid regular expression",
	UnknownKeyword:     "unknown keyword",
	UnsupportedKeyword: "unsupported keyword",
	InvalidAtKey:       "invalid At key",
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

// Schema error categories. Each concrete failure is an *Error whose Err is one
// of these, so errors.Is(err, ErrKeyword) still classifies it.
var (
	ErrKeyword        = errors.New("invalid keyword value")
	ErrUnknownKeyword = errors.New("unknown keyword")
	ErrUnsupported    = errors.New("unsupported keyword")
	ErrPattern        = errors.New("invalid pattern")
	ErrRef            = errors.New("unresolved ref")
	ErrOption         = errors.New("invalid option")
)

func (e *Error) Error() string { return fmt.Sprintf("%v (%v)", e.Err, e.Diag.Code) }
func (e *Error) Unwrap() error { return e.Err }

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
	off, end := clampSpan(d.Off, d.End, len(src))

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
		return "invalid document"
	case 1:
		return "invalid document: " + e[0].Code.String()
	default:
		return fmt.Sprintf("invalid document: %s (+%d more)", e[0].Code.String(), len(e)-1)
	}
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

// AsError wraps diags as an *Invalid, or returns a nil error when there are none,
// so propagating a validation result stays a one-liner.
func AsError(diags []Diag) error {
	if len(diags) == 0 {
		return nil
	}

	return Diagnostics(diags)
}

// AsDiag returns the diagnostics carried by an Invalid anywhere in err's chain,
// or nil when err carries none.
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

// serr builds a schema Error: a diagnostic (the classifying code, the offending
// keyword op — None if none — and its span in the schema source, off plus length
// n stored as a half-open Off/End) and a category sentinel. Specifics beyond the
// code are recovered from op and the span, or carried in kind.
func serr(code DiagCode, op Opcode, off, n int, kind error) *Error {
	return &Error{Diag: Diag{Code: code, Op: op.Op(), Off: off, End: off + n}, Err: kind}
}
