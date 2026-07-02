package schema

import (
	"errors"

	"nikand.dev/go/json2"
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

// Error is a schema failure the caller can show and classify: Message is a
// curated, user-safe sentence, Op is the offending keyword (None when none), and
// Off/Len is its span in the schema source. Err is the category sentinel, so
// errors.Is(err, ErrPattern) matches. Error keeps only what is safe to display;
// low-level causes (regexp, JSON offsets) are folded into Message when they help
// the user and dropped otherwise, never leaked through Err.
type Error struct {
	Message  string // user-facing detail, safe to display
	Op       Opcode // offending keyword, None when none applies
	Off, Len int    // offending span in the schema source
	Err      error  // category sentinel: ErrKeyword / ErrPattern / ErrRef / ...
}

func (e *Error) Error() string { return e.Err.Error() + ": " + e.Message }
func (e *Error) Unwrap() error { return e.Err }

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
// (None if none) and its span in the schema source, and a category sentinel.
func serr(msg string, op Opcode, off, n int, kind error) *Error {
	return &Error{Message: msg, Op: op.Op(), Off: off, Len: n, Err: kind}
}
