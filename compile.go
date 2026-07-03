package schema

import (
	"fmt"
	"regexp"
	"strings"

	"nikand.dev/go/json2"
)

type (
	def struct {
		name string // full pointer, e.g. "#/$defs/Name"
		root Opcode
	}
)

const (
	typeNull = 1 << iota
	typeBool
	typeInt
	typeNum
	typeStr
	typeArr
	typeObj

	typeErr
)

// Compile parses a schema document into a program.
func Compile(b []byte) (*Schema, error) {
	var s Schema

	err := s.Compile(b)
	if err != nil {
		return nil, err
	}

	return &s, nil
}

func (s *Schema) Compile(schema []byte) error {
	b := &s.prog

	b.src = schema
	b.code = b.code[:0]
	b.text = b.text[:0]
	b.tmp = b.tmp[:0]

	s.defs = s.defs[:0]
	clear(s.patterns)

	if b.text == nil {
		b.text = b.textbuf[:]
	}

	var d json2.Iterator

	root, i, err := s.compile(schema, 0)
	if err != nil {
		return normSyntax(err)
	}

	i = d.SkipSpaces(schema, i)
	if i != len(schema) {
		return ErrTrailingData
	}

	s.root = root
	s.rootID()
	s.register()

	if err := s.checkRefs(); err != nil {
		return err
	}

	return s.checkPatterns()
}

// AddDoc registers doc under uri so an external $ref to that document resolves to
// it. The registry is shared into doc, so documents can refer to each other.
// Register before Compile of any document that refs uri.
func (s *Schema) AddDoc(uri string, doc *Schema) {
	if s.docs == nil {
		s.docs = map[string]*Schema{}
	}

	doc.id = uri
	doc.docs = s.docs
	s.docs[uri] = doc
}

// register puts this document into the shared registry under its own id, so
// other documents (including lazily loaded ones) can resolve back to it. The
// registry is created on demand once a Resolve hook is present.
func (s *Schema) register() {
	if s.docs == nil {
		if s.Resolve == nil {
			return
		}

		s.docs = map[string]*Schema{}
	}

	if s.id != "" {
		s.docs[s.id] = s
	}
}

// rootID reads the document's own base URI from a top-level $id, kept as a Raw.
func (s *Schema) rootID() {
	if s.root.Op() != All {
		return
	}

	for _, ch := range s.prog.Reader().Nodes(s.root) {
		if ch.Op() != Raw {
			continue
		}

		if string(s.prog.Reader().String(s.prog.code[ch.Off()])) == "$id" {
			s.id = string(s.prog.Reader().String(s.prog.code[ch.Off()+1]))
			return
		}
	}
}

func (s *Schema) compile(b []byte, st int) (Opcode, int, error) {
	var d json2.Iterator

	tp, i, err := d.Type(b, st)
	if err != nil {
		return 0, i, err
	}

	switch tp {
	case json2.Object:
		return s.object(b, i)
	case json2.Bool:
		op := Fail
		if b[i] == 't' {
			op = Pass
		}

		i, err = d.Skip(b, i)
		return op, i, err
	default:
		return 0, i, serr("a schema must be an object or a boolean", None, i, 0, ErrKeyword)
	}
}

func (s *Schema) object(b []byte, st int) (Opcode, int, error) {
	mark := len(s.prog.tmp)
	defer func() { s.prog.tmp = s.prog.tmp[:mark] }()

	var d json2.Iterator

	i, err := d.Enter(b, st, json2.Object)
	if err != nil {
		return 0, i, err
	}

	var key []byte
	var op, anchor Opcode
	var hasAnchor bool

	for d.ForMore(b, &i, json2.Object, &err) {
		kst := i

		key, i, err = d.Key(b, i)
		if err != nil {
			return 0, i, err
		}

		op, i, err = s.keyword(key, b, kst, i)
		if err != nil {
			return 0, i, err
		}

		if op != Pass {
			s.prog.tmp = append(s.prog.tmp, op)
		}

		if string(key) == "$anchor" {
			anchor, hasAnchor = op, true // op is the Raw{key,val}; val is the anchor name
		}
	}
	if err != nil {
		return 0, i, err
	}

	s.mergeDefs(mark)

	if !s.Flags.Is(SchemaKeepOrder) {
		s.canonRequired(s.prog.tmp[mark:])
		sortKeywords(s.prog.tmp[mark:])
	}

	s.linkAdditional(s.prog.tmp[mark:])

	n := len(s.prog.tmp) - mark
	off := len(s.prog.code)
	s.prog.code = append(s.prog.code, s.prog.tmp[mark:]...)

	node := makeNode(All, off, n)

	if hasAnchor {
		name := string(s.prog.Reader().String(s.prog.code[anchor.Off()+1]))
		s.defs = append(s.defs, def{"#" + name, node})
	}

	return node, i, nil
}

func (s *Schema) keyword(name, b []byte, kst, st int) (Opcode, int, error) {
	switch string(name) {
	case "type":
		return s.kwType(b, st)
	case "properties":
		return s.kwProps(b, st)
	case "patternProperties":
		return s.kwPatternProps(b, st)
	case "required":
		return s.kwList(Required, b, st)
	case "enum":
		return s.kwList(Enum, b, st)
	case "const":
		return s.kwValue(Const, b, st)
	case "default":
		return s.kwValue(Default, b, st)
	case "minimum":
		return s.kwNum(Minimum, b, st)
	case "maximum":
		return s.kwNum(Maximum, b, st)
	case "exclusiveMinimum":
		return s.kwNum(ExclMin, b, st)
	case "exclusiveMaximum":
		return s.kwNum(ExclMax, b, st)
	case "multipleOf":
		return s.kwNum(MultipleOf, b, st)
	case "items":
		return s.kwSub(Items, b, st)
	case "additionalProperties":
		return s.kwSub(Additional, b, st)
	case "not":
		return s.kwSub(Not, b, st)
	case "allOf":
		return s.kwSchemas(AllOf, b, st)
	case "anyOf":
		return s.kwSchemas(AnyOf, b, st)
	case "oneOf":
		return s.kwSchemas(OneOf, b, st)
	case "minLength":
		return s.kwImm(MinLen, b, st)
	case "maxLength":
		return s.kwImm(MaxLen, b, st)
	case "minItems":
		return s.kwImm(MinItems, b, st)
	case "maxItems":
		return s.kwImm(MaxItems, b, st)
	case "minProperties":
		return s.kwImm(MinProps, b, st)
	case "maxProperties":
		return s.kwImm(MaxProps, b, st)
	case "uniqueItems":
		return s.kwUnique(b, st)
	case "pattern":
		return s.kwPattern(b, st)
	case "$ref":
		return s.kwRef(b, st)
	case "$defs", "definitions":
		return s.kwDefs(name, b, st)
	default:
		return s.kwUnknown(name, b, kst, st)
	}
}

func (s *Schema) kwType(b []byte, st int) (Opcode, int, error) {
	var d json2.Iterator

	tp, i, err := d.Type(b, st)
	if err != nil {
		return 0, i, err
	}

	var mask int
	var name []byte

	switch tp {
	case json2.String:
		name, i, err = d.Key(b, i)
		if err != nil {
			return 0, i, err
		}

		mask = typeBit(name)
	case json2.Array:
		i, err = d.Enter(b, i, json2.Array)
		if err != nil {
			return 0, i, err
		}

		for d.ForMore(b, &i, json2.Array, &err) {
			name, i, err = d.Key(b, i)
			if err != nil {
				return 0, i, err
			}

			mask |= typeBit(name)
		}
		if err != nil {
			return 0, i, err
		}
	default:
		return 0, i, serr(`"type" must be a string or array of type names`, Type, st, i-st, ErrKeyword)
	}

	if mask&typeErr != 0 {
		return 0, i, serr(`"type" contains an unknown type name`, Type, st, i-st, ErrKeyword)
	}

	return makeImm(Type, mask), i, nil
}

// enterKind opens a container-keyword value, returning a curated ErrKeyword when
// the value isn't the expected object/array instead of json2's raw type error.
func (s *Schema) enterKind(b []byte, st int, typ json2.Type, op Opcode, want string) (int, error) {
	var d json2.Iterator

	tp, i, err := d.Type(b, st)
	if err != nil {
		return i, err
	}

	if tp != typ {
		return i, serr(fmt.Sprintf("%q must be %s", keywordName(op), want), op, st, i-st, ErrKeyword)
	}

	return d.Enter(b, st, typ)
}

func (s *Schema) kwProps(b []byte, st int) (Opcode, int, error) {
	mark := len(s.prog.tmp)
	defer func() { s.prog.tmp = s.prog.tmp[:mark] }()

	var d json2.Iterator

	i, err := s.enterKind(b, st, json2.Object, Properties, "an object")
	if err != nil {
		return 0, i, err
	}

	var key, sub Opcode

	for d.ForMore(b, &i, json2.Object, &err) {
		key, i, err = s.literal(b, i)
		if err != nil {
			return 0, i, err
		}

		sub, i, err = s.compile(b, i)
		if err != nil {
			return 0, i, err
		}

		s.prog.tmp = append(s.prog.tmp, key, sub)
	}
	if err != nil {
		return 0, i, err
	}

	n := (len(s.prog.tmp) - mark) / 2
	off := len(s.prog.code)
	s.prog.code = append(s.prog.code, s.prog.tmp[mark:]...)

	return makeNode(Properties, off, n), i, nil
}

// kwPatternProps parses patternProperties: a regex key (stored as a Pattern
// span, compiled by checkPatterns like any other) paired with a subschema.
func (s *Schema) kwPatternProps(b []byte, st int) (Opcode, int, error) {
	mark := len(s.prog.tmp)
	defer func() { s.prog.tmp = s.prog.tmp[:mark] }()

	var d json2.Iterator

	i, err := s.enterKind(b, st, json2.Object, PatternProps, "an object")
	if err != nil {
		return 0, i, err
	}

	var pat, sub Opcode

	for d.ForMore(b, &i, json2.Object, &err) {
		pat, i, err = s.kwPattern(b, i)
		if err != nil {
			return 0, i, err
		}

		sub, i, err = s.compile(b, i)
		if err != nil {
			return 0, i, err
		}

		s.prog.tmp = append(s.prog.tmp, pat, sub)
	}
	if err != nil {
		return 0, i, err
	}

	n := (len(s.prog.tmp) - mark) / 2
	off := len(s.prog.code)
	s.prog.code = append(s.prog.code, s.prog.tmp[mark:]...)

	return makeNode(PatternProps, off, n), i, nil
}

func (s *Schema) kwList(op Opcode, b []byte, st int) (Opcode, int, error) {
	mark := len(s.prog.tmp)
	defer func() { s.prog.tmp = s.prog.tmp[:mark] }()

	var d json2.Iterator

	i, err := s.enterKind(b, st, json2.Array, op, "an array")
	if err != nil {
		return 0, i, err
	}

	var val Opcode

	for d.ForMore(b, &i, json2.Array, &err) {
		est := i

		val, i, err = s.literal(b, i)
		if err != nil {
			return 0, i, err
		}

		if op == Required && val.Op() != Str {
			return 0, i, serr(`"required" entries must be strings`, Required, est, i-est, ErrKeyword)
		}

		s.prog.tmp = append(s.prog.tmp, val)
	}
	if err != nil {
		return 0, i, err
	}

	n := len(s.prog.tmp) - mark
	off := len(s.prog.code)
	s.prog.code = append(s.prog.code, s.prog.tmp[mark:]...)

	return makeNode(op, off, n), i, nil
}

func (s *Schema) kwSchemas(op Opcode, b []byte, st int) (Opcode, int, error) {
	mark := len(s.prog.tmp)
	defer func() { s.prog.tmp = s.prog.tmp[:mark] }()

	var d json2.Iterator

	i, err := s.enterKind(b, st, json2.Array, op, "an array")
	if err != nil {
		return 0, i, err
	}

	var sub Opcode

	for d.ForMore(b, &i, json2.Array, &err) {
		sub, i, err = s.compile(b, i)
		if err != nil {
			return 0, i, err
		}

		s.prog.tmp = append(s.prog.tmp, sub)
	}
	if err != nil {
		return 0, i, err
	}

	n := len(s.prog.tmp) - mark
	off := len(s.prog.code)
	s.prog.code = append(s.prog.code, s.prog.tmp[mark:]...)

	return makeNode(op, off, n), i, nil
}

func (s *Schema) kwSub(op Opcode, b []byte, st int) (Opcode, int, error) {
	sub, i, err := s.compile(b, st)
	if err != nil {
		return 0, i, err
	}

	off := len(s.prog.code)
	s.prog.code = append(s.prog.code, sub)

	return makeNode(op, off, 1), i, nil
}

func (s *Schema) kwValue(op Opcode, b []byte, st int) (Opcode, int, error) {
	val, i, err := s.literal(b, st)
	if err != nil {
		return 0, i, err
	}

	off := len(s.prog.code)
	s.prog.code = append(s.prog.code, val)

	return makeNode(op, off, 1), i, nil
}

// kwNum is kwValue for numeric keywords: the value must be a JSON number, so a
// typo like {"minimum":"x"} is a curated ErrKeyword instead of a silently-zero
// bound. The decoder already classifies the literal, so val.Op() is the check.
func (s *Schema) kwNum(op Opcode, b []byte, st int) (Opcode, int, error) {
	val, i, err := s.literal(b, st)
	if err != nil {
		return 0, i, err
	}

	if val.Op() != Num {
		return 0, i, serr(fmt.Sprintf("%q must be a number", keywordName(op)), op, st, i-st, ErrKeyword)
	}

	off := len(s.prog.code)
	s.prog.code = append(s.prog.code, val)

	return makeNode(op, off, 1), i, nil
}

func (s *Schema) kwImm(op Opcode, b []byte, st int) (Opcode, int, error) {
	var d json2.Iterator

	raw, i, err := d.Raw(b, st)
	if err != nil {
		return 0, i, err
	}

	n, err := json2.Value(raw).Int()
	if err != nil {
		return 0, i, serr(fmt.Sprintf("%q must be an integer", keywordName(op)), op, st, i-st, ErrKeyword)
	}

	return makeImm(op, n), i, nil
}

func (s *Schema) kwUnique(b []byte, st int) (Opcode, int, error) {
	var d json2.Iterator

	raw, i, err := d.Raw(b, st)
	if err != nil {
		return 0, i, err
	}

	v, err := json2.Value(raw).Bool()
	if err != nil {
		return 0, i, serr(`"uniqueItems" must be a boolean`, Unique, st, i-st, ErrKeyword)
	}

	if !v {
		return Pass, i, nil
	}

	return Unique, i, nil
}

func (s *Schema) kwPattern(b []byte, st int) (Opcode, int, error) {
	var d json2.Iterator

	tp, i, err := d.Type(b, st)
	if err != nil {
		return 0, i, err
	}

	if tp != json2.String {
		return 0, i, serr(`"pattern" must be a string`, Pattern, st, i-st, ErrKeyword)
	}

	j, err := d.Skip(b, i)
	if err != nil {
		return 0, j, err
	}

	return makeNode(Pattern, i, j-i), j, nil
}

func (s *Schema) kwRef(b []byte, st int) (Opcode, int, error) {
	var d json2.Iterator

	tp, i, err := d.Type(b, st)
	if err != nil {
		return 0, i, err
	}

	if tp != json2.String {
		return 0, i, serr(`"$ref" must be a string`, Ref, st, i-st, ErrKeyword)
	}

	j, err := d.Skip(b, i)
	if err != nil {
		return 0, j, err
	}

	// any URI-reference: "#..." internal, "doc#frag" external (resolved via docs).
	off, n := i+1, j-i-2 // strip the quotes
	if n < 1 {
		return 0, i, serr(`"$ref" must not be empty`, Ref, st, j-st, ErrKeyword)
	}

	return makeNode(Ref, off, n), j, nil
}

// pointerEscape encodes a definition name into a JSON Pointer reference token:
// '~'->"~0", '/'->"~1" (order matters), so "a/b" stored as "a~1b" is comparable
// to a $ref pointer and never ambiguous with a navigation step.
func pointerEscape(s string) string {
	if !strings.ContainsAny(s, "~/") {
		return s
	}

	s = strings.ReplaceAll(s, "~", "~0")
	s = strings.ReplaceAll(s, "/", "~1")

	return s
}

// kwDefs compiles $defs/definitions into a Defs pair-block (raw key + subschema)
// that Format round-trips, and registers each entry in the resolution table
// s.defs under its canonical pointer name for $ref lookup.
func (s *Schema) kwDefs(name, b []byte, st int) (Opcode, int, error) {
	mark := len(s.prog.tmp)
	defer func() { s.prog.tmp = s.prog.tmp[:mark] }()

	var d json2.Iterator

	i, err := s.enterKind(b, st, json2.Object, Defs, "an object")
	if err != nil {
		return 0, i, err
	}

	prefix := "#/" + string(name) + "/"

	var key, sub Opcode

	for d.ForMore(b, &i, json2.Object, &err) {
		key, i, err = s.literal(b, i)
		if err != nil {
			return 0, i, err
		}

		sub, i, err = s.compile(b, i)
		if err != nil {
			return 0, i, err
		}

		s.prog.tmp = append(s.prog.tmp, key, sub)
		s.defs = append(s.defs, def{prefix + pointerEscape(string(s.prog.Reader().String(key))), sub})
	}
	if err != nil {
		return 0, i, err
	}

	n := (len(s.prog.tmp) - mark) / 2
	off := len(s.prog.code)
	s.prog.code = append(s.prog.code, s.prog.tmp[mark:]...)

	return makeNode(Defs, off, n), i, nil
}

// kwUnknown keeps a keyword we don't model as a pair node for round-trip: an Ext
// for a custom "x-" keyword (a Walk handler can spot it and act), else an inert
// Raw annotation. A non-"x-" unknown is rejected under SchemaRejectUnknown.
func (s *Schema) kwUnknown(name, b []byte, kst, st int) (Opcode, int, error) {
	op := Raw
	switch {
	case isExtKeyword(name):
		op = Ext
	case !knownKeyword(name) && s.Flags.Is(SchemaRejectUnknown):
		return 0, st, serr(fmt.Sprintf("%q", name), None, kst, st-kst, ErrUnknownKeyword)
	}

	var d json2.Iterator

	kend, err := d.Skip(b, kst)
	if err != nil {
		return 0, kend, err
	}

	key := makeNode(Str, kst, kend-kst)

	val, i, err := s.literal(b, st)
	if err != nil {
		return 0, i, err
	}

	off := len(s.prog.code)
	s.prog.code = append(s.prog.code, key, val)

	return makeNode(op, off, 2), i, nil
}

// isExtKeyword reports whether name is a custom "x-" keyword (x- plus a suffix).
func isExtKeyword(name []byte) bool {
	return len(name) >= 3 && name[0] == 'x' && name[1] == '-'
}

// checkRefs validates refs without any I/O: internal pointers must resolve now,
// external refs to a registered document are checked now, and external refs left
// to the Resolve hook are deferred to apply (error here only if neither applies).
func (s *Schema) checkRefs() error {
	for _, op := range s.prog.code {
		if op.Op() != Ref {
			continue
		}

		ref := string(s.prog.Reader().Span(op))
		doc, frag := splitRef(ref)

		if doc == "" {
			if s.fragTarget(frag) == bad {
				return serr(fmt.Sprintf("%q", ref), op, op.OffInt(), op.ArgInt(), ErrRef)
			}

			continue
		}

		if t := s.docs[doc]; t != nil {
			if t.fragTarget(frag) == bad {
				return serr(fmt.Sprintf("%q", ref), op, op.OffInt(), op.ArgInt(), ErrRef)
			}

			continue
		}

		if s.Resolve == nil {
			return serr(fmt.Sprintf("no resolver for %q", ref), op, op.OffInt(), op.ArgInt(), ErrRef)
		}
	}

	return nil
}

// checkPatterns compiles every pattern node up front, so a bad regex is a
// schema error and apply can match without compiling or failing.
func (s *Schema) checkPatterns() error {
	for _, op := range s.prog.code {
		if op.Op() != Pattern {
			continue
		}

		var d json2.Iterator

		src, _, err := d.DecodeString(s.prog.Reader().Span(op), 0, nil)
		if err != nil {
			return err
		}

		re, err := regexp.Compile(string(src))
		if err != nil {
			reason := strings.TrimPrefix(err.Error(), "error parsing regexp: ")
			msg := fmt.Sprintf("%q is not a valid regular expression: %s", src, reason)
			return serr(msg, op, op.OffInt(), op.ArgInt(), ErrPattern)
		}

		if s.patterns == nil {
			s.patterns = map[Opcode]*regexp.Regexp{}
		}

		s.patterns[op] = re
	}

	return nil
}

// refResolve resolves a $ref to its document and node: the same schema for an
// internal "#frag", another document for "doc#frag". The doc part is an opaque
// handle matched against the registry, then loaded via Resolve on a miss.
func (s *Schema) refResolve(op Opcode) (*Schema, Opcode, error) {
	ref := string(s.prog.Reader().Span(op))
	doc, frag := splitRef(ref)

	t := s

	if doc != "" {
		var err error

		t, err = s.loadDoc(doc)
		if err != nil {
			return s, bad, err
		}
	}

	tnode := t.fragTarget(frag)
	if tnode == bad {
		return s, bad, serr(fmt.Sprintf("%q", ref), op, op.OffInt(), op.ArgInt(), ErrRef)
	}

	return t, tnode, nil
}

// loadDoc returns the document for an opaque handle: from the registry, or via
// the Resolve hook (compiled and cached under the handle). The registry and hook
// are shared into the loaded doc so it can resolve its own external refs.
func (s *Schema) loadDoc(handle string) (*Schema, error) {
	if t := s.docs[handle]; t != nil {
		return t, nil
	}

	if s.Resolve == nil {
		return nil, serr(fmt.Sprintf("no resolver for %q", handle), None, 0, 0, ErrRef)
	}

	body, err := s.Resolve(s.id, handle)
	if err != nil {
		return nil, err
	}

	t := &Schema{docs: s.docs, Resolve: s.Resolve}

	if err := t.Compile(body); err != nil {
		return nil, err
	}

	s.docs[handle] = t

	return t, nil
}

// splitRef cuts a ref at '#' into (document, fragment); fragment keeps the '#'.
func splitRef(ref string) (doc, frag string) {
	if i := strings.IndexByte(ref, '#'); i >= 0 {
		return ref[:i], ref[i:]
	}

	return ref, ""
}

// fragTarget resolves a fragment within this document: "" or "#" is the root,
// "#/$defs/x" and "#anchor" are entries in the defs table.
func (s *Schema) fragTarget(frag string) Opcode {
	if frag == "" || frag == "#" {
		return s.root
	}

	for i := range s.defs {
		if s.defs[i].name == frag {
			return s.defs[i].root
		}
	}

	return bad
}

func (s *Schema) literal(b []byte, st int) (Opcode, int, error) {
	return s.prog.value(b, st, false)
}

func typeBit(name []byte) int {
	switch string(name) {
	case "null":
		return typeNull
	case "boolean":
		return typeBool
	case "integer":
		return typeInt
	case "number":
		return typeNum
	case "string":
		return typeStr
	case "array":
		return typeArr
	case "object":
		return typeObj
	default:
		return typeErr
	}
}

func knownKeyword(name []byte) bool {
	switch string(name) {
	case "$schema", "$id", "$anchor", "$comment", "$vocabulary",
		"title", "description", "examples", "readOnly", "writeOnly", "deprecated":
		return true
	case "if", "then", "else", "contains", "minContains", "maxContains",
		"propertyNames", "prefixItems",
		"dependentSchemas", "dependentRequired", "dependencies",
		"unevaluatedItems", "unevaluatedProperties":
		return true
	case "format", "contentEncoding", "contentMediaType", "contentSchema":
		return true
	case "$dynamicRef", "$dynamicAnchor", "$recursiveRef", "$recursiveAnchor":
		return true
	}

	return len(name) >= 2 && name[0] == 'x' && name[1] == '-'
}

var keywordOrder = []Opcode{
	Ref,
	Type, Enum, Const,
	Minimum, Maximum, ExclMin, ExclMax, MultipleOf,
	MinLen, MaxLen, Pattern,
	MinItems, MaxItems, Unique, Items,
	MinProps, MaxProps, Properties, Required, PatternProps, Additional,
	Not, AllOf, AnyOf, OneOf,
	Default,
	Defs,
}

// mergeDefs folds several $defs/definitions blocks in one object into a single
// Defs node, so Format emits one "$defs" member with unique JSON keys.
func (s *Schema) mergeDefs(mark int) {
	tmp := s.prog.tmp
	first := -1

	for i := mark; i < len(tmp); i++ {
		if tmp[i].Op() != Defs {
			continue
		}

		if first < 0 {
			first = i
			continue
		}

		a, b := tmp[first], tmp[i]
		off := len(s.prog.code)
		s.prog.code = append(s.prog.code, s.prog.Reader().Nodes(a)...)
		s.prog.code = append(s.prog.code, s.prog.Reader().Nodes(b)...)
		tmp[first] = makeNode(Defs, off, int(a.Arg()+b.Arg()))

		copy(tmp[i:], tmp[i+1:])
		tmp = tmp[:len(tmp)-1]
		i--
	}

	s.prog.tmp = tmp
}

// linkAdditional gives an additionalProperties node references to its sibling
// properties and patternProperties nodes, so apply can tell which keys are
// already covered. Without either sibling the node keeps its lone subschema
// (every property is additional).
func (s *Schema) linkAdditional(and []Opcode) {
	var props, patterns Opcode
	ai := -1

	for i, op := range and {
		switch op.Op() {
		case Properties:
			props = op
		case PatternProps:
			patterns = op
		case Additional:
			ai = i
		}
	}

	if ai < 0 || (props.Op() != Properties && patterns.Op() != PatternProps) {
		return
	}

	off := len(s.prog.code)
	s.prog.code = append(s.prog.code, props, patterns, s.prog.code[and[ai].Off()])

	and[ai] = makeNode(Additional, off, 3)
}

// additionalParts splits an Additional node into its sibling properties and
// patternProperties nodes (Pass when absent) and its subschema.
func (s *Schema) additionalParts(op Opcode) (props, patterns, sub Opcode) {
	if op.Arg() == 3 {
		o := op.Off()
		return s.prog.code[o], s.prog.code[o+1], s.prog.code[o+2]
	}

	return Pass, Pass, s.prog.code[op.Off()]
}

func (s *Schema) canonRequired(and []Opcode) {
	var props, req Opcode

	for _, op := range and {
		switch op.Op() {
		case Properties:
			props = op
		case Required:
			req = op
		}
	}

	if props.Op() != Properties || req.Op() != Required {
		return
	}

	names := s.prog.Reader().Nodes(req)

	for i := 1; i < len(names); i++ {
		for j := i; j > 0 && s.propIndex(props, names[j]) < s.propIndex(props, names[j-1]); j-- {
			names[j], names[j-1] = names[j-1], names[j]
		}
	}
}

func (s *Schema) propIndex(props, name Opcode) int {
	off, n := props.OffInt(), props.ArgInt()

	for i := range n {
		if string(s.prog.Reader().Span(s.prog.code[off+2*i])) == string(s.prog.Reader().Span(name)) {
			return i
		}
	}

	return n
}

func sortKeywords(and []Opcode) {
	for i := 1; i < len(and); i++ {
		for j := i; j > 0 && keywordRank(and[j]) < keywordRank(and[j-1]); j-- {
			and[j], and[j-1] = and[j-1], and[j]
		}
	}
}

func keywordRank(op Opcode) int {
	for i, k := range keywordOrder {
		if k == op.Op() {
			return i
		}
	}

	return len(keywordOrder)
}
