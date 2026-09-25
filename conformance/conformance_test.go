package conformance

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"nikand.dev/go/schema"
)

type suiteGroup struct {
	Description string          `json:"description"`
	Schema      json.RawMessage `json:"schema"`
	Tests       []suiteCase     `json:"tests"`
}

type suiteCase struct {
	Description string          `json:"description"`
	Data        json.RawMessage `json:"data"`
	Valid       bool            `json:"valid"`
}

// unsupported marks suite entries we skip for a reason the strict compile can't
// express on its own — the $ref families that need a resolver we don't wire into
// this harness. Keyed by file base name, or "file.json: group description".
// Recognized-but-unimplemented keywords are NOT listed here: strict compile
// refuses them on its own (see rejectsCleanly), which is the honest signal.
var unsupported = map[string]string{
	"ref.json":        "needs $ref resolution wiring",
	"dynamicRef.json": "needs $ref resolution wiring",
	"refRemote.json":  "needs remote resolver",
	"anchor.json":     "needs $anchor resolution wiring",
	"vocabulary.json": "needs $vocabulary handling",
	"defs.json: validate definition against metaschema": "needs metaschema resolver",
}

// unimplementedFormats marks the optional/format files naming a format we do not
// assert. An unimplemented name is an inert annotation, so their valid cases
// would pass vacuously and their invalid ones fail; naming them here says which
// format is missing. unknown.json is not among them: asserting that an unknown
// format is ignored is exactly what we do.
var unimplementedFormats = map[string]string{
	"duration.json":              "duration not implemented",
	"hostname.json":              "hostname dropped: needs IDNA tables for the idn sibling",
	"idn-email.json":             "idn-email not implemented",
	"idn-hostname.json":          "idn-hostname not implemented",
	"iri.json":                   "iri not implemented",
	"iri-reference.json":         "iri-reference not implemented",
	"uri.json":                   "uri not implemented",
	"uri-reference.json":         "uri-reference not implemented",
	"uri-template.json":          "uri-template not implemented",
	"json-pointer.json":          "json-pointer not implemented",
	"relative-json-pointer.json": "relative-json-pointer not implemented",
	"regex.json":                 "regex not implemented",
	"ecmascript-regex.json":      "format regex, and the ECMA-262 dialect at that",
}

// strict is the compile flag set the suites run under: a keyword we do not know
// or do not implement is a compile error, not a silent pass.
const strict = schema.SchemaRejectUnknown | schema.SchemaRejectUnsupported

// formatStrict drops SchemaRejectUnsupported: an unimplemented format value is
// the spec's own annotation case, and the files that name one are skipped by
// name anyway, so refusing it by flag would only hide unknown.json — which
// asserts exactly the behaviour we have.
const formatStrict = strict &^ schema.SchemaRejectUnsupported

func TestConformance(tb *testing.T) {
	passed, ran, rejected, skipped := runSuite(tb, "testdata/suite/tests/*.json", strict, unsupported, suiteValid)

	tb.Logf("draft 2020-12: passed %d / ran %d, rejected %d (unimplemented), skipped %d",
		passed, ran, rejected, skipped)
}

func TestFormatConformance(tb *testing.T) {
	const glob = "testdata/suite/tests/optional/format/*.json"

	passed, ran, rejected, skipped := runSuite(tb, glob, formatStrict|schema.AssertStringFormat, unimplementedFormats, suiteValid)

	tb.Logf("format: passed %d / ran %d, rejected %d (unimplemented), skipped %d",
		passed, ran, rejected, skipped)

	// without the flag "format" is an annotation, so every case validates
	passed, ran, rejected, skipped = runSuite(tb, glob, formatStrict, unimplementedFormats, alwaysValid)

	tb.Logf("format as annotation: passed %d / ran %d, rejected %d (unimplemented), skipped %d",
		passed, ran, rejected, skipped)
}

// TestEmailUseful pins AssertEmailUseful, which the suite cannot: it tests the
// RFC 5321 reading, where a quoted local part and an address literal are valid.
func TestEmailUseful(tb *testing.T) {
	for _, tc := range []struct {
		data       string
		rfc, usefl bool
	}{
		{`"joe.bloggs@example.com"`, true, true},
		{`"a+tag@sub.example.co.uk"`, true, true},
		{`"\"joe bloggs\"@example.com"`, true, false},
		{`"\"joe..bloggs\"@example.com"`, true, false},
		{`"\"joe@bloggs\"@example.com"`, true, false},
		{`"joe@[127.0.0.1]"`, true, false},
		{`"joe@[IPv6:::1]"`, true, false},
		{`"joe bloggs@example.com"`, false, false},
		{`"@example.com"`, false, false},
	} {
		for _, useful := range []bool{false, true} {
			flags := strict | schema.AssertStringFormat
			want := tc.rfc

			if useful {
				flags |= schema.AssertEmailUseful
				want = tc.usefl
			}

			var s schema.Schema
			s.Flags.Set(flags)

			if err := s.Compile([]byte(`{"format":"email"}`)); err != nil {
				tb.Fatalf("compile: %v", err)
			}

			var a schema.Applier

			d, err := a.Validate(&s, []byte(tc.data))
			if err != nil {
				tb.Errorf("validate %s: %v", tc.data, err)
				continue
			}

			if (len(d) == 0) != want {
				tb.Errorf("validate %s useful=%v: ok=%v, want %v", tc.data, useful, len(d) == 0, want)
			}
		}
	}
}

func suiteValid(c suiteCase) bool { return c.Valid }
func alwaysValid(suiteCase) bool  { return true }

// runSuite compiles every group in the globbed files with flags and checks each
// case against want. skip names a file, or a "file.json: group description", to
// leave out with its cases counted as skipped.
func runSuite(tb *testing.T, glob string, flags schema.Flags, skip map[string]string, want func(suiteCase) bool) (passed, ran, rejected, skipped int) {
	tb.Helper()

	files, err := filepath.Glob(glob)
	if err != nil || len(files) == 0 {
		tb.Fatalf("suite glob %s: %v (%d files)", glob, err, len(files))
	}

	var a schema.Applier

	for _, f := range files {
		base := filepath.Base(f)

		raw, err := os.ReadFile(f)
		if err != nil {
			tb.Fatal(err)
		}

		var groups []suiteGroup
		if err := json.Unmarshal(raw, &groups); err != nil {
			tb.Fatalf("%s: %v", base, err)
		}

		for _, g := range groups {
			key := base + ": " + g.Description

			if _, off := skip[base]; off {
				skipped += len(g.Tests)
				continue
			}
			if _, off := skip[key]; off {
				skipped += len(g.Tests)
				continue
			}

			var s schema.Schema
			s.Flags.Set(flags)
			cerr := s.Compile(g.Schema)

			if cerr != nil {
				if !rejectsCleanly(cerr) {
					tb.Errorf("%s: compile: %v", key, cerr)
					continue
				}

				rejected += len(g.Tests)
				continue
			}

			for _, c := range g.Tests {
				ran++

				if validates(&a, &s, c) == want(c) {
					passed++
					continue
				}

				tb.Errorf("%s / %s: valid=%v want %v", key, c.Description, !want(c), want(c))
			}
		}
	}

	return passed, ran, rejected, skipped
}

// rejectsCleanly reports whether err is an honest "we don't implement this":
// classifiable as ErrUnsupported/ErrUnknownKeyword and carrying the naming
// diagnostic, so a caller can tell it apart from a real compile failure.
func rejectsCleanly(err error) bool {
	if !errors.Is(err, schema.ErrUnsupported) && !errors.Is(err, schema.ErrUnknownKeyword) {
		return false
	}

	d := schema.AsDiag(err)
	if len(d) != 1 {
		return false
	}

	switch d[0].Code {
	case schema.UnknownKeyword, schema.UnsupportedKeyword, schema.UnsupportedFormat:
		return true
	}

	return false
}

func validates(a *schema.Applier, s *schema.Schema, c suiteCase) bool {
	diag, err := a.Validate(s, c.Data)
	return err == nil && len(diag) == 0
}
