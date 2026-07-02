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

// Schema errors. Each is wrapped with context (the offending keyword or ref) via
// fmt.Errorf, so errors.Is still matches the sentinel.
var (
	ErrKeyword        = errors.New("invalid keyword value") // wrong type/shape/range for a keyword
	ErrUnknownKeyword = errors.New("unknown keyword")       // under SchemaRejectUnknown
	ErrPattern        = errors.New("invalid pattern")       // pattern/patternProperties won't compile
	ErrRef            = errors.New("unresolved ref")        // $ref/$anchor target missing or unloadable
)
