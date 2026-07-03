package schema

import (
	"errors"
	"fmt"

	"nikand.dev/go/json2"
)

type (
	// Error is a schema failure the caller can show and classify: Message is a
	// curated, user-safe sentence, Op is the offending keyword (None when none), and
	// Off/End is its half-open span in the schema source. Err is the category
	// sentinel, so errors.Is(err, ErrPattern) matches. Error keeps only what is safe
	// to display; low-level causes (regexp, JSON offsets) are folded into Message
	// when they help the user and dropped otherwise, never leaked through Err.
	Error struct {
		Message  string // user-facing detail, safe to display
		Op       Opcode // offending keyword, None when none applies
		Off, End int    // offending half-open span in the schema source
		Err      error  // category sentinel: ErrKeyword / ErrPattern / ErrRef / ...
	}

	Diag struct {
		Off, End int    // offending half-open span in the input (see cur.span)
		Op       Opcode // failed keyword
		Msg      string
	}
)

// JSON-shape errors, reused from the decoder.
var (
	ErrSyntax       = json2.ErrSyntax       // not well-formed JSON
	ErrTrailingData = json2.ErrTrailingData // extra bytes after the value
)

// Schema error categories. Each concrete failure is an *Error whose Err is one
// of these, so errors.Is(err, ErrKeyword) still classifies it.
var (
	ErrKeyword        = errors.New("invalid keyword value") // wrong type/shape/range for a keyword
	ErrUnknownKeyword = errors.New("unknown keyword")       // under SchemaRejectUnknown
	ErrPattern        = errors.New("invalid pattern")       // pattern/patternProperties won't compile
	ErrRef            = errors.New("unresolved ref")        // $ref/$anchor target missing or unloadable
)

func (e *Error) Error() string { return e.Err.Error() + ": " + e.Message }
func (e *Error) Unwrap() error { return e.Err }

// Invalid carries validation diagnostics as an error, so a caller can return
// them through a plain error result and recover them higher up the stack with
// errors.As(err, &inv). Diagnostics are not errors on their own — Validate
// returns them alongside a nil error; wrap them in Invalid only to propagate.
type Invalid []Diag

func (e Invalid) Error() string {
	switch len(e) {
	case 0:
		return "invalid document"
	case 1:
		return "invalid document: " + e[0].Msg
	default:
		return fmt.Sprintf("invalid document: %s (+%d more)", e[0].Msg, len(e)-1)
	}
}

// AsError wraps diags as an *Invalid, or returns a nil error when there are none,
// so propagating a validation result stays a one-liner.
func AsError(diags []Diag) error {
	if len(diags) == 0 {
		return nil
	}

	return Invalid(diags)
}

// AsDiag returns the diagnostics carried by an Invalid anywhere in err's chain,
// or nil when err carries none.
func AsDiag(err error) []Diag {
	var inv Invalid
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

// serr builds a schema Error: a user-facing message, the offending keyword op
// (None if none) and its span in the schema source (off plus length n, stored as
// a half-open Off/End), and a category sentinel.
func serr(msg string, op Opcode, off, n int, kind error) *Error {
	return &Error{Message: msg, Op: op.Op(), Off: off, End: off + n, Err: kind}
}
