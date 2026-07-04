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

func TestConformance(tb *testing.T) {
	files, err := filepath.Glob("testdata/suite/tests/*.json")
	if err != nil || len(files) == 0 {
		tb.Fatalf("suite glob: %v (%d files)", err, len(files))
	}

	var passed, ran, rejected, skipped int

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

			if _, off := unsupported[base]; off {
				skipped += len(g.Tests)
				continue
			}
			if _, off := unsupported[key]; off {
				skipped += len(g.Tests)
				continue
			}

			var s schema.Schema
			s.Flags.Set(schema.SchemaRejectUnknown | schema.SchemaRejectUnsupported)
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

				if validates(&s, c) == c.Valid {
					passed++
					continue
				}

				tb.Errorf("%s / %s: valid=%v want %v", key, c.Description, !c.Valid, c.Valid)
			}
		}
	}

	tb.Logf("draft 2020-12: passed %d / ran %d, rejected %d (unimplemented), skipped %d",
		passed, ran, rejected, skipped)
}

// rejectsCleanly reports whether err is an honest "we don't implement this":
// classifiable as ErrUnsupported/ErrUnknownKeyword and carrying a named *Error,
// so a caller can tell it apart from a real compile failure.
func rejectsCleanly(err error) bool {
	if !errors.Is(err, schema.ErrUnsupported) && !errors.Is(err, schema.ErrUnknownKeyword) {
		return false
	}

	var e *schema.Error
	return errors.As(err, &e) && e.Message != ""
}

func validates(s *schema.Schema, c suiteCase) bool {
	diag, err := s.Validate(c.Data)
	return err == nil && len(diag) == 0
}
