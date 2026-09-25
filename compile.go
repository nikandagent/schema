package schema

import (
	"bytes"
	"math"
	"regexp"
	"strings"

	"nikand.dev/go/json2"
)

type (
	def struct {
		name string // full pointer, e.g. "#/$defs/Name"
		root Node
	}
)

func MustCompile(b []byte) *Schema {
	s := new(Schema)
	s.MustCompile(b)
	return s
}

// Compile parses a schema document into a program.
func Compile(b []byte) (*Schema, error) {
	s := new(Schema)

	err := s.Compile(b)
	if err != nil {
		return nil, err
	}

	return s, nil
}

func (s *Schema) MustCompile(schema []byte) {
	err := s.Compile(schema)
	if err != nil {
		panic(err)
	}
}

func (s *Schema) Compile(schema []byte) error {
	b := &s.prog
	b.Reset()

	b.src = schema

	if s.defs == nil {
		s.defs = s.defsbuf[:]
	}

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

	doc.ID = uri
	doc.docs = s.docs
	s.docs[uri] = doc
}

// register puts this document into the shared registry under its own id, so
// other documents (including lazily loaded ones) can resolve back to it. The
// registry is created on demand once a Resolve hook is present.
func (s *Schema) register() {
	if s.docs == nil {
		// A registry is worth its allocation for a document that can be reached
		// by name — its own, or another one it may fetch. A document with
		// neither only ever follows refs within itself.
		if s.Resolve == nil && s.ID == "" {
			return
		}

		s.docs = map[string]*Schema{}
	}

	if s.ID != "" {
		s.docs[s.ID] = s
	}
}

// rootID reads the document's own base URI from a top-level $id.
func (s *Schema) rootID() {
	if s.root.Op() != All {
		return
	}

	if id := s.prog.Reader().Keyword(s.root, ID); id.op != None {
		s.ID = string(s.prog.Reader().String(id))
	}
}

func (s *Schema) compile(b []byte, st int) (Node, int, error) {
	var d json2.Iterator

	tp, i, err := d.Type(b, st)
	if err != nil {
		return Node{}, i, err
	}

	switch tp {
	case json2.Object:
		return s.object(b, i)
	case json2.Bool:
		op := Node{op: Fail}
		if b[i] == 't' {
			op = Node{op: Pass}
		}

		i, err = d.Skip(b, i)
		return op, i, err
	default:
		return Node{}, i, kerr(SchemaMustBeObject, None)
	}
}

func (s *Schema) object(b []byte, st int) (Node, int, error) {
	mark := len(s.prog.tmp)
	defer func() { s.prog.tmp = s.prog.tmp[:mark] }()

	var d json2.Iterator

	i, err := d.Enter(b, st, json2.Object)
	if err != nil {
		return Node{}, i, err
	}

	var key []byte
	var op, anchor, id Node
	var hasAnchor bool

	for d.ForMore(b, &i, json2.Object, &err) {
		kst := i

		key, i, err = d.Key(b, i)
		if err != nil {
			return Node{}, i, err
		}

		vst := i

		op, i, err = s.keyword(key, b, kst, i)
		if err != nil {
			return Node{}, i, locate(err, kst, s.pairEnd(b, vst, i))
		}

		// Every keyword remembers the "key": value pair it was written as, so a
		// finding can point back into the schema text.
		op = op.withSrc(kst, i)

		if op.op != Pass {
			s.prog.tmp = append(s.prog.tmp, op)
		}

		switch string(key) {
		case "$anchor":
			anchor, hasAnchor = op, true // op is the Raw{key,val}; val is the anchor name
		case "$id":
			id = op // an ID node: the URI is its own string
		}
	}
	if err != nil {
		return Node{}, i, err
	}

	s.mergeDefs(mark)

	if !s.Flags.Is(SchemaKeepOrder) {
		s.canonRequired(s.prog.tmp[mark:])
		s.sortKeywords(s.prog.tmp[mark:])
	}

	s.linkAdditional(s.prog.tmp[mark:])
	s.linkCond(s.prog.tmp[mark:])
	s.linkItems(s.prog.tmp[mark:])

	n := len(s.prog.tmp) - mark
	off := len(s.prog.code)
	s.prog.code = append(s.prog.code, s.prog.tmp[mark:]...)

	node := makeNode(All, off, n).withSrc(st, i)

	if hasAnchor {
		frag := "#" + string(s.prog.Reader().String(s.prog.code[anchor.Off()+1]))

		if s.fragTarget(frag).op != None {
			return Node{}, i, serr(DuplicateAnchor, anchor)
		}

		s.defs = append(s.defs, def{frag, node})
	}

	// A schema that names itself is a resource of its own: register the URI so a
	// $ref to it resolves here instead of going out to the registry. The root
	// also becomes the document's name, in rootID.
	if id.op != None {
		uri := string(s.prog.Reader().String(id))

		if s.fragTarget(uri).op != None {
			return Node{}, i, serr(DuplicateID, id)
		}

		s.defs = append(s.defs, def{uri, node})
	}

	return node, i, nil
}

func (s *Schema) keyword(name, b []byte, kst, st int) (Node, int, error) {
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
	case "prefixItems":
		return s.kwSchemas(Prefix, b, st)
	case "additionalProperties":
		return s.kwSub(Additional, b, st)
	case "not":
		return s.kwSub(Not, b, st)
	case "if":
		return s.kwSub(If, b, st)
	case "then":
		return s.kwSub(Then, b, st)
	case "else":
		return s.kwSub(Else, b, st)
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
	case "format":
		return s.kwFormat(name, b, kst, st)
	case "$id":
		return s.kwID(b, st)
	case "$ref":
		return s.kwRef(b, st)
	case "$defs", "definitions":
		return s.kwDefs(name, b, st)
	default:
		return s.kwUnknown(name, b, kst, st)
	}
}

func (s *Schema) kwType(b []byte, st int) (Node, int, error) {
	var d json2.Iterator

	tp, i, err := d.Type(b, st)
	if err != nil {
		return Node{}, i, err
	}

	var mask Types
	var name []byte

	switch tp {
	case json2.String:
		name, i, err = d.Key(b, i)
		if err != nil {
			return Node{}, i, err
		}

		mask = typeBit(name)
	case json2.Array:
		i, err = d.Enter(b, i, json2.Array)
		if err != nil {
			return Node{}, i, err
		}

		for d.ForMore(b, &i, json2.Array, &err) {
			name, i, err = d.Key(b, i)
			if err != nil {
				return Node{}, i, err
			}

			mask |= typeBit(name)
		}
		if err != nil {
			return Node{}, i, err
		}
	default:
		return Node{}, i, kerr(InvalidTypeShape, Type)
	}

	if mask&typeErr != 0 {
		return Node{}, i, kerr(UnknownType, Type)
	}

	return makeImm(Type, int(mask)), i, nil
}

// enterKind opens a container-keyword value, returning a curated ErrKeyword when
// the value isn't the expected object/array instead of json2's raw type error.
func (s *Schema) enterKind(b []byte, st int, typ json2.Type, op Opcode) (int, error) {
	var d json2.Iterator

	tp, i, err := d.Type(b, st)
	if err != nil {
		return i, err
	}

	if tp != typ {
		code := MustBeObject
		if typ == json2.Array {
			code = MustBeArray
		}

		return i, kerr(code, op)
	}

	return d.Enter(b, st, typ)
}

func (s *Schema) kwProps(b []byte, st int) (Node, int, error) {
	mark := len(s.prog.tmp)
	defer func() { s.prog.tmp = s.prog.tmp[:mark] }()

	var d json2.Iterator

	i, err := s.enterKind(b, st, json2.Object, Properties)
	if err != nil {
		return Node{}, i, err
	}

	var key, sub Node

	for d.ForMore(b, &i, json2.Object, &err) {
		key, i, err = s.literal(b, i)
		if err != nil {
			return Node{}, i, err
		}

		sub, i, err = s.compile(b, i)
		if err != nil {
			return Node{}, i, err
		}

		s.prog.tmp = append(s.prog.tmp, key, sub)
	}
	if err != nil {
		return Node{}, i, err
	}

	n := (len(s.prog.tmp) - mark) / 2
	off := len(s.prog.code)
	s.prog.code = append(s.prog.code, s.prog.tmp[mark:]...)

	return makeNode(Properties, off, n), i, nil
}

// kwPatternProps parses patternProperties: a regex key (stored as a Pattern
// span, compiled by checkPatterns like any other) paired with a subschema.
func (s *Schema) kwPatternProps(b []byte, st int) (Node, int, error) {
	mark := len(s.prog.tmp)
	defer func() { s.prog.tmp = s.prog.tmp[:mark] }()

	var d json2.Iterator

	i, err := s.enterKind(b, st, json2.Object, PatternProps)
	if err != nil {
		return Node{}, i, err
	}

	var pat, sub Node

	for d.ForMore(b, &i, json2.Object, &err) {
		pat, i, err = s.kwPattern(b, i)
		if err != nil {
			return Node{}, i, err
		}

		sub, i, err = s.compile(b, i)
		if err != nil {
			return Node{}, i, err
		}

		s.prog.tmp = append(s.prog.tmp, pat, sub)
	}
	if err != nil {
		return Node{}, i, err
	}

	n := (len(s.prog.tmp) - mark) / 2
	off := len(s.prog.code)
	s.prog.code = append(s.prog.code, s.prog.tmp[mark:]...)

	return makeNode(PatternProps, off, n), i, nil
}

func (s *Schema) kwList(op Opcode, b []byte, st int) (Node, int, error) {
	mark := len(s.prog.tmp)
	defer func() { s.prog.tmp = s.prog.tmp[:mark] }()

	var d json2.Iterator

	i, err := s.enterKind(b, st, json2.Array, op)
	if err != nil {
		return Node{}, i, err
	}

	var val Node

	for d.ForMore(b, &i, json2.Array, &err) {
		val, i, err = s.literal(b, i)
		if err != nil {
			return Node{}, i, err
		}

		if op == Required && val.Op() != String {
			return Node{}, i, kerr(RequiredNotString, Required)
		}

		s.prog.tmp = append(s.prog.tmp, val)
	}
	if err != nil {
		return Node{}, i, err
	}

	n := len(s.prog.tmp) - mark
	off := len(s.prog.code)
	s.prog.code = append(s.prog.code, s.prog.tmp[mark:]...)

	return makeNode(op, off, n), i, nil
}

func (s *Schema) kwSchemas(op Opcode, b []byte, st int) (Node, int, error) {
	mark := len(s.prog.tmp)
	defer func() { s.prog.tmp = s.prog.tmp[:mark] }()

	var d json2.Iterator

	i, err := s.enterKind(b, st, json2.Array, op)
	if err != nil {
		return Node{}, i, err
	}

	var sub Node

	for d.ForMore(b, &i, json2.Array, &err) {
		sub, i, err = s.compile(b, i)
		if err != nil {
			return Node{}, i, err
		}

		s.prog.tmp = append(s.prog.tmp, sub)
	}
	if err != nil {
		return Node{}, i, err
	}

	n := len(s.prog.tmp) - mark
	off := len(s.prog.code)
	s.prog.code = append(s.prog.code, s.prog.tmp[mark:]...)

	return makeNode(op, off, n), i, nil
}

func (s *Schema) kwSub(op Opcode, b []byte, st int) (Node, int, error) {
	sub, i, err := s.compile(b, st)
	if err != nil {
		return Node{}, i, err
	}

	off := len(s.prog.code)
	s.prog.code = append(s.prog.code, sub)

	return makeNode(op, off, 1), i, nil
}

func (s *Schema) kwValue(op Opcode, b []byte, st int) (Node, int, error) {
	val, i, err := s.literal(b, st)
	if err != nil {
		return Node{}, i, err
	}

	off := len(s.prog.code)
	s.prog.code = append(s.prog.code, val)

	return makeNode(op, off, 1), i, nil
}

// kwNum is kwValue for numeric keywords: the value must be a JSON number, so a
// typo like {"minimum":"x"} is a curated ErrKeyword instead of a silently-zero
// bound. The decoder already classifies the literal, so val.Op() is the check.
func (s *Schema) kwNum(op Opcode, b []byte, st int) (Node, int, error) {
	val, i, err := s.literal(b, st)
	if err != nil {
		return Node{}, i, err
	}

	if val.Op() != Number {
		return Node{}, i, kerr(MustBeNumber, op)
	}

	off := len(s.prog.code)
	s.prog.code = append(s.prog.code, val)

	return makeNode(op, off, 1), i, nil
}

func (s *Schema) kwImm(op Opcode, b []byte, st int) (Node, int, error) {
	var d json2.Iterator

	raw, i, err := d.Raw(b, st)
	if err != nil {
		return Node{}, i, err
	}

	n, ok := integerValue(raw)
	if !ok {
		return Node{}, i, kerr(MustBeInteger, op)
	}

	return makeImm(op, n), i, nil
}

// integerValue reads a JSON number that denotes an integer, accepting an
// integer-valued decimal like 2.0 (which the spec treats as 2).
func integerValue(raw []byte) (int, bool) {
	if n, err := json2.Value(raw).Int(); err == nil {
		return n, true
	}

	f, err := json2.Value(raw).Float64()
	if err != nil || f != math.Trunc(f) {
		return 0, false
	}

	return int(f), true
}

func (s *Schema) kwUnique(b []byte, st int) (Node, int, error) {
	var d json2.Iterator

	raw, i, err := d.Raw(b, st)
	if err != nil {
		return Node{}, i, err
	}

	v, err := json2.Value(raw).Bool()
	if err != nil {
		return Node{}, i, kerr(MustBeBool, Unique)
	}

	if !v {
		return makeImm(Unique, 0), i, nil
	}

	return makeImm(Unique, 1), i, nil
}

// kwFormat compiles a format we assert into one word. Any other name stays an
// annotation, kept verbatim: the spec has a validator ignore what it does not
// know rather than fail, and the document still round-trips.
func (s *Schema) kwFormat(name, b []byte, kst, st int) (Node, int, error) {
	var d json2.Iterator

	tp, i, err := d.Type(b, st)
	if err != nil {
		return Node{}, i, err
	}

	if tp != json2.String {
		return Node{}, i, kerr(MustBeString, Format)
	}

	fname, i, err := d.Key(b, i)
	if err != nil {
		return Node{}, i, err
	}

	f := formatOf(fname)
	if f == 0 {
		// A format we do not implement: the spec's default is to carry it as an
		// annotation, so keep the pair. It is the keyword's value we lack, not
		// the keyword — only RejectUnsupported, which asks about anything
		// unimplemented, turns it into an error.
		if s.Flags.Is(SchemaRejectUnsupported) {
			return Node{}, i, kerr(UnsupportedFormat, Format)
		}

		return s.kwPair(Raw, b, kst, st)
	}

	return makeImm(Format, int(f)), i, nil
}

func (s *Schema) kwPattern(b []byte, st int) (Node, int, error) {
	var d json2.Iterator

	tp, i, err := d.Type(b, st)
	if err != nil {
		return Node{}, i, err
	}

	if tp != json2.String {
		return Node{}, i, kerr(MustBeString, Pattern)
	}

	j, err := d.Skip(b, i)
	if err != nil {
		return Node{}, j, err
	}

	op, err := s.prog.str(b, i, j, Pattern, false)

	return op, j, err
}

// kwID is the subschema's own name: an opaque URI, the handle a $ref to this
// subschema uses. It is a keyword, not an annotation, because resolution needs
// to find it — ordered first in the block so it sits at index 0.
func (s *Schema) kwID(b []byte, st int) (Node, int, error) {
	return s.kwString(ID, b, st)
}

func (s *Schema) kwRef(b []byte, st int) (Node, int, error) {
	return s.kwString(Ref, b, st)
}

// kwString compiles a keyword whose value is a non-empty string kept verbatim:
// any URI-reference for $ref ("#..." internal, "doc#frag" external), the URI
// itself for $id.
func (s *Schema) kwString(op Opcode, b []byte, st int) (Node, int, error) {
	var d json2.Iterator

	tp, i, err := d.Type(b, st)
	if err != nil {
		return Node{}, i, err
	}

	if tp != json2.String {
		return Node{}, i, kerr(MustBeString, op)
	}

	j, err := d.Skip(b, i)
	if err != nil {
		return Node{}, j, err
	}

	if j-i-2 < 1 { // nothing between the quotes
		return Node{}, i, kerr(EmptyRef, op)
	}

	node, err := s.prog.str(b, i, j, op, false)
	if err != nil {
		return Node{}, j, err
	}

	return node, j, nil
}

// refString is the pointer a Ref denotes.
func (s *Schema) refString(op Node) string {
	return string(s.prog.Reader().String(op))
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
func (s *Schema) kwDefs(name, b []byte, st int) (Node, int, error) {
	mark := len(s.prog.tmp)
	defer func() { s.prog.tmp = s.prog.tmp[:mark] }()

	var d json2.Iterator

	i, err := s.enterKind(b, st, json2.Object, Defs)
	if err != nil {
		return Node{}, i, err
	}

	prefix := "#/" + string(name) + "/"

	var key, sub Node

	for d.ForMore(b, &i, json2.Object, &err) {
		key, i, err = s.literal(b, i)
		if err != nil {
			return Node{}, i, err
		}

		sub, i, err = s.compile(b, i)
		if err != nil {
			return Node{}, i, err
		}

		s.prog.tmp = append(s.prog.tmp, key, sub)
		s.defs = append(s.defs, def{prefix + pointerEscape(string(s.prog.Reader().String(key))), sub})
	}
	if err != nil {
		return Node{}, i, err
	}

	n := (len(s.prog.tmp) - mark) / 2
	off := len(s.prog.code)
	s.prog.code = append(s.prog.code, s.prog.tmp[mark:]...)

	return makeNode(Defs, off, n), i, nil
}

// kwUnknown keeps a keyword we don't model as a pair node for round-trip: an Ext
// for a custom "x-" keyword (a Walk handler can spot it and act), else an inert
// Raw annotation. A recognized-but-unimplemented keyword is rejected under
// SchemaRejectUnsupported (ErrUnsupported); a genuine unknown under
// SchemaRejectUnknown (ErrUnknownKeyword).
func (s *Schema) kwUnknown(name, b []byte, kst, st int) (Node, int, error) {
	op := Raw
	switch {
	case isExtKeyword(name):
		op = Ext
	case unsupportedKeyword(name):
		if s.Flags.Is(SchemaRejectUnsupported) {
			return Node{}, st, kerr(UnsupportedKeyword, None)
		}
	case annotationKeyword(name):
		// inert even under strict: a legit no-op in our vocabulary
	case s.Flags.Is(SchemaRejectUnknown):
		return Node{}, st, kerr(UnknownKeyword, None)
	}

	return s.kwPair(op, b, kst, st)
}

// kwPair keeps a keyword we do not model as a key/value pair node, so it
// round-trips and a Walk handler can act on it.
func (s *Schema) kwPair(op Opcode, b []byte, kst, st int) (Node, int, error) {
	var d json2.Iterator

	kend, err := d.Skip(b, kst)
	if err != nil {
		return Node{}, kend, err
	}

	key, err := s.prog.str(b, kst, kend, String, false)
	if err != nil {
		return Node{}, kend, err
	}

	val, i, err := s.literal(b, st)
	if err != nil {
		return Node{}, i, err
	}

	off := len(s.prog.code)
	s.prog.code = append(s.prog.code, key, val)

	return makeNode(op, off, 1), i, nil // one key/value pair
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

		doc, frag := splitRef(s.refString(op))

		if doc == "" {
			if s.fragTarget(frag).op == None {
				return serr(UnresolvedRef, op)
			}

			continue
		}

		if n := s.fragTarget(doc); n.op != None {
			if s.fragFrom(n, frag).op == None {
				return serr(UnresolvedRef, op)
			}

			continue
		}

		if t := s.docs[doc]; t != nil {
			if t.fragTarget(frag).op == None {
				return serr(UnresolvedRef, op)
			}

			continue
		}

		if s.Resolve == nil {
			return serr(NoResolver, op)
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

		re, err := regexp.Compile(string(s.prog.Reader().Span(op)))
		if err != nil {
			return serr(BadPattern, op)
		}

		if s.patterns == nil {
			s.patterns = map[Node]*regexp.Regexp{}
		}

		s.patterns[op] = re
	}

	return nil
}

// Lookup resolves a $ref string the way a $ref keyword would: "#frag" in this
// document, "doc#frag" in a registered or Resolve-loaded one. The returned
// document owns the node, so walk or read it through that document.
func (s *Schema) Lookup(ref string) (*Schema, Node, error) {
	return s.lookup(ref, Node{})
}

// RefTarget is Lookup of the pointer a $ref node holds, errors anchored at the
// node.
func (s *Schema) RefTarget(op Node) (*Schema, Node, error) {
	if op.Op() != Ref {
		panic(op.Op())
	}

	return s.lookup(s.refString(op), op)
}

func (s *Schema) lookup(ref string, op Node) (*Schema, Node, error) {
	doc, frag := splitRef(ref)

	t := s

	if doc != "" {
		// A schema in this document may have named itself doc with $id; then the
		// fragment is a pointer into that schema, not into the root.
		if n := s.fragTarget(doc); n.op != None {
			n = s.fragFrom(n, frag)
			if n.op == None {
				return s, Node{}, serr(UnresolvedRef, op)
			}

			return s, n, nil
		}

		var err error

		t, err = s.loadDoc(doc)
		if err != nil {
			return s, Node{}, err
		}
	}

	tnode := t.fragTarget(frag)
	if tnode.op == None {
		return s, Node{}, serr(UnresolvedRef, op)
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
		return nil, serr(NoResolver, Node{})
	}

	body, err := s.Resolve(s.ID, handle)
	if err != nil {
		return nil, err
	}

	// Name it before compiling: the handle is the retrieval URI, which is the
	// base for its own refs unless its text overrides with a $id.
	t := &Schema{ID: handle, docs: s.docs, Resolve: s.Resolve}

	if err := t.Compile(body); err != nil {
		return nil, err
	}

	s.docs[handle] = t

	return t, nil
}

// pairEnd is where the keyword's value ends. A keyword that rejects its value
// on sight stops at the value's first byte, so skip it to name the whole pair;
// a value the decoder cannot skip either is reported as far as it got.
func (s *Schema) pairEnd(b []byte, vst, i int) int {
	var d json2.Iterator

	if end, err := d.Skip(b, vst); err == nil && end > i {
		return end
	}

	return i
}

// locate points a compile error at the whole "key": value pair it came from,
// the way a compiled keyword node remembers itself. The innermost level that
// knows a pair wins: once a finding has a place, the levels above leave it.
func locate(err error, off, end int) error {
	d, ok := err.(Diagnostics)
	if !ok || len(d) != 1 || d[0].Op.meta != 0 {
		return err
	}

	d[0].Op = d[0].Op.withSrc(off, end)

	return d
}

// splitRef cuts a ref at '#' into (document, fragment); fragment keeps the '#'.
func splitRef(ref string) (doc, frag string) {
	if i := strings.IndexByte(ref, '#'); i >= 0 {
		return ref[:i], ref[i:]
	}

	return ref, ""
}

// fragTarget resolves a fragment against this document's root.
func (s *Schema) fragTarget(frag string) Node {
	return s.fragFrom(s.root, frag)
}

// fragFrom resolves a fragment against the schema start names: "" or "#" is
// start itself, "#anchor" and "#/$defs/x" are entries in the defs table (which
// are document-wide, so they ignore start), any other "#/..." is a pointer
// walked from start.
func (s *Schema) fragFrom(start Node, frag string) Node {
	if frag == "" || frag == "#" {
		return start
	}

	for i := range s.defs {
		if s.defs[i].name == frag {
			return s.defs[i].root
		}
	}

	if !strings.HasPrefix(frag, "#/") {
		return Node{}
	}

	return s.pointerFrom(start, frag[1:])
}

// pointerTarget walks a JSON Pointer over the program: a keyword name in a
// schema object, a member name in a properties-like block, an index in a schema
// list. A single-subschema keyword (items, not, if, ...) is stepped through so
// the pointer reads as it does over the JSON. Only a schema position resolves.
func (s *Schema) pointerFrom(start Node, p string) Node {
	r := s.prog.Reader()
	op := start

	for p != "" {
		var tok string
		tok, p = pointerToken(p)

		switch op.Op() {
		case All:
			op = s.keywordNamed(op, tok)
		case Properties, PatternProps, Defs:
			op = r.Find(op, tok)
		case AllOf, AnyOf, OneOf, Prefix:
			i, ok := pointerIndex(tok)
			if !ok || i >= op.ArgInt() {
				return Node{}
			}

			op = r.Nodes(op)[i]
		default:
			return Node{}
		}

		switch op.Op() {
		case Items:
			_, op = s.prog.Reader().ItemsParts(op)
		case Additional:
			_, _, op = s.prog.Reader().PropertiesParts(op)
		case If:
			op, _, _ = s.prog.Reader().CondParts(op)
		case Not, Then, Else:
			op = r.Deref(op)
		}
	}

	switch op.Op() {
	case All, Pass, Fail:
		return op
	default:
		return Node{}
	}
}

// keywordNamed is the keyword of schema node op spelled name, or None. Raw and
// Ext hold literals, never a schema, so they are not looked up.
func (s *Schema) keywordNamed(op Node, name string) Node {
	for _, c := range s.prog.Reader().Nodes(op) {
		if c.Op() != Raw && c.Op() != Ext && c.Keyword() == name {
			return c
		}
	}

	return Node{}
}

// pointerToken splits the leading reference token off p ("/a/b" -> "a", "/b"),
// decoding "~1" to '/' and then "~0" to '~' (RFC 6901 order).
func pointerToken(p string) (tok, rest string) {
	p = p[1:]

	if i := strings.IndexByte(p, '/'); i >= 0 {
		tok, rest = p[:i], p[i:]
	} else {
		tok = p
	}

	if strings.IndexByte(tok, '~') < 0 {
		return tok, rest
	}

	tok = strings.ReplaceAll(tok, "~1", "/")
	tok = strings.ReplaceAll(tok, "~0", "~")

	return tok, rest
}

// pointerIndex reads an array index token: digits only, no leading zero.
func pointerIndex(tok string) (int, bool) {
	if tok == "" || len(tok) > 1 && tok[0] == '0' {
		return 0, false
	}

	n := 0

	for _, c := range []byte(tok) {
		if c < '0' || c > '9' {
			return 0, false
		}

		n = n*10 + int(c-'0')
	}

	return n, true
}

func (s *Schema) literal(b []byte, st int) (Node, int, error) {
	return s.prog.value(b, st, false)
}

func typeBit(name []byte) Types {
	switch string(name) {
	case "null":
		return TypeNull
	case "boolean":
		return TypeBoolean
	case "integer":
		return TypeInteger
	case "number":
		return TypeNumber
	case "string":
		return TypeString
	case "array":
		return TypeArray
	case "object":
		return TypeObject
	default:
		return typeErr
	}
}

// unsupportedKeyword reports whether name is a real 2020-12 assertion keyword we
// don't implement yet — kept inert for round-trip, but rejected with
// ErrUnsupported under SchemaRejectUnsupported.
func unsupportedKeyword(name []byte) bool {
	switch string(name) {
	case "contains", "minContains", "maxContains",
		"propertyNames",
		"dependentSchemas", "dependentRequired", "dependencies",
		"unevaluatedItems", "unevaluatedProperties",
		"$dynamicRef", "$dynamicAnchor", "$recursiveRef", "$recursiveAnchor":
		return true
	}

	return false
}

// annotationKeyword reports whether name is a known keyword that is a legitimate
// no-op in our vocabulary (metadata, or annotation-only under 2020-12) — kept
// inert even under strict flags.
func annotationKeyword(name []byte) bool {
	switch string(name) {
	case "$schema", "$anchor", "$comment", "$vocabulary",
		"title", "description", "examples", "readOnly", "writeOnly", "deprecated",
		"contentEncoding", "contentMediaType", "contentSchema":
		return true
	}

	return false
}

var keywordOrder = []Opcode{
	ID,
	Ref,
	Type, Ext,
	Enum, Const,
	Minimum, Maximum, ExclMin, ExclMax, MultipleOf,
	MinLen, MaxLen, Pattern, Format,
	MinItems, MaxItems, Unique, Prefix, Items,
	MinProps, MaxProps, Properties, Required, PatternProps, Additional,
	Not, AllOf, AnyOf, OneOf,
	If, Then, Else,
	Default,
	Defs,
	Raw,
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
func (s *Schema) linkAdditional(all []Node) {
	var props, patterns Node
	ai := -1

	for i, op := range all {
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
	s.prog.code = append(s.prog.code, props, patterns, s.prog.code[all[ai].Off()])

	all[ai] = makeNode(Additional, off, 3)
}

// PropertiesParts splits the properties family, which the compiler folded into
// the additionalProperties node: the properties and patternProperties nodes
// (Pass when absent) and the subschema for every member neither of them covers,
// which is Fail for additionalProperties:false and Pass for true.
func (b BufferReader) PropertiesParts(op Node) (props, patterns, sub Node) {
	if op.Arg() == 3 {
		o := op.OffInt()
		return b.code[o], b.code[o+1], b.code[o+2]
	}

	return Node{op: Pass}, Node{op: Pass}, b.code[op.OffInt()]
}

// linkItems gives an items node a reference to its sibling prefixItems, so apply
// knows how many leading items are already covered.
func (s *Schema) linkItems(all []Node) {
	var prefix Node
	ii := -1

	for i, op := range all {
		switch op.Op() {
		case Prefix:
			prefix = op
		case Items:
			ii = i
		}
	}

	if ii < 0 || prefix.Op() != Prefix {
		return
	}

	off := len(s.prog.code)
	s.prog.code = append(s.prog.code, s.prog.code[all[ii].Off()], prefix)

	all[ii] = makeNode(Items, off, 2)
}

// ItemsParts splits an items node into its sibling prefixItems (Pass when
// absent), which covers the leading elements, and the subschema for the rest.
//
// Pass says a part places no constraint, whether it was absent or written true;
// ask Keyword for the keyword itself to tell those apart.
func (b BufferReader) ItemsParts(op Node) (prefix, sub Node) {
	if op.Arg() == 2 {
		o := op.OffInt()
		return b.code[o+1], b.code[o]
	}

	return Node{op: Pass}, b.code[op.OffInt()]
}

func (s *Schema) linkCond(all []Node) {
	ii, ti, ei := -1, -1, -1

	for i, op := range all {
		switch op.Op() {
		case If:
			ii = i
		case Then:
			ti = i
		case Else:
			ei = i
		}
	}

	if ii < 0 || (ti < 0 && ei < 0) {
		return
	}

	then, els := Node{op: Pass}, Node{op: Pass}

	if ti >= 0 {
		then = s.prog.code[all[ti].Off()]
	}
	if ei >= 0 {
		els = s.prog.code[all[ei].Off()]
	}

	off := len(s.prog.code)
	s.prog.code = append(s.prog.code, s.prog.code[all[ii].Off()], then, els)

	all[ii] = makeNode(If, off, 3)
}

// CondParts splits an if node into its condition and the then and else arms
// (Pass when absent), which the compiler folded into it.
func (b BufferReader) CondParts(op Node) (cond, then, els Node) {
	off := op.OffInt()

	if op.Arg() == 3 {
		return b.code[off], b.code[off+1], b.code[off+2]
	}

	return b.code[off], Node{op: Pass}, Node{op: Pass}
}

func (s *Schema) canonRequired(all []Node) {
	var props, req Node

	for _, op := range all {
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

func (s *Schema) propIndex(props, name Node) int {
	off, n := props.OffInt(), props.ArgInt()

	for i := range n {
		if bytes.Equal(s.prog.Reader().Span(s.prog.code[off+2*i]), s.prog.Reader().Span(name)) {
			return i
		}
	}

	return n
}

func (s *Schema) sortKeywords(all []Node) {
	for i := 1; i < len(all); i++ {
		for j := i; j > 0 && s.keywordLess(all[j], all[j-1]); j-- {
			all[j], all[j-1] = all[j-1], all[j]
		}
	}
}

// keywordLess orders keywords by keywordRank, breaking ties by name for the only
// repeatable keywords, Ext and Raw (every other keyword is unique per schema).
func (s *Schema) keywordLess(a, b Node) bool {
	ra, rb := s.keywordRank(a), s.keywordRank(b)
	if ra != rb {
		return ra < rb
	}

	if a.Op() != Ext && a.Op() != Raw {
		return false
	}

	return string(s.prog.Reader().Span(s.prog.code[a.Off()])) < string(s.prog.Reader().Span(s.prog.code[b.Off()]))
}

// rawFront are annotation keywords we keep as Raw (no dedicated opcode) yet order
// ahead of the real keywords; any other Raw ranks last, in Raw's keywordOrder slot.
var rawFront = []string{"title", "description"}

func (s *Schema) keywordRank(op Node) int {
	if op.Op() == Raw {
		name := s.prog.Reader().String(s.prog.code[op.Off()])

		for i, k := range rawFront {
			if string(name) == k {
				return i
			}
		}
	}

	for i, k := range keywordOrder {
		if k == op.Op() {
			return len(rawFront) + i
		}
	}

	return len(rawFront) + len(keywordOrder)
}
