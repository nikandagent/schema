package schema

import (
	"bytes"
	"errors"
	"math"
	"unicode/utf8"

	"nikand.dev/go/json2"
)

type (
	// Step is one subschema entry in the descent. Op is the keyword entered
	// through, Value names the way in — the property name, the pattern, the
	// branch index, the ref pointer — and Sub is the subschema reached. Doc is
	// the document Sub lives in, which changes only at a $ref. DataKey is the
	// data key or index the value descended by, Node{} when the data stayed put,
	// which is what tells an in-place applicator from a real step.
	Step struct {
		Doc   *Schema
		Op    Node
		Value Node
		Sub   Node

		DataKey Node
	}

	Applier struct {
		// Buffer is the data arena the walk decodes into and rewrites through.
		Buffer Buffer

		// Diags is every finding so far. The engine only appends to it or
		// truncates it — a trial branch rewinds it whole — so an index is stable
		// while it lives, and a handler that filters out a middle entry breaks
		// that for everyone downstream.
		Diags []Diag

		// Steps is the descent from the root: one per subschema entered,
		// including the ones the data does not follow ($ref, allOf, if). Live
		// during the walk only; copy what you keep.
		Steps []Step

		// Depth is how far the data descended: the number of steps that moved it.
		Depth int

		dbuf [5]Diag // inline room for the first findings, filling the 1280 bucket

		rewrite bool
		save    bool // copy Steps into every Diag
	}

	// Handler is called per node during a Walk. s is the document the node
	// lives in, which changes at a $ref. It receives the handler to delegate
	// with (normally itself, so children reach the handler too) and passes it
	// on to Apply — pass nil to run a subtree with default behaviour only, or a
	// different Handler to swap behaviour for that subtree.
	Handler func(a *Applier, s *Schema, op, val Node, h Handler) (Node, error)
)

// ErrBreak is returned by a Handler to stop the walk cleanly.
var ErrBreak = errors.New("break")

// Validate runs s over doc, reporting what does not hold. The Applier is the
// workspace: its Diags, Steps and Buffer are readable until the next run.
func (a *Applier) Validate(s *Schema, doc []byte) ([]Diag, error) {
	return a.Walk(s, Node{}, doc, nil)
}

// Walk validates doc against node op of s — Node{} for the root, any subschema
// for a fragment — calling h for every node it visits.
func (a *Applier) Walk(s *Schema, op Node, doc []byte, h Handler) ([]Diag, error) {
	_, err := a.walk(s, op, doc, h, false)
	if err != nil {
		return nil, err
	}

	return a.Diags, nil
}

// Rewrite validates doc and appends its canonical form to buf: key order, filled
// defaults and normalized whitespace, per s.Flags.
func (a *Applier) Rewrite(s *Schema, op Node, doc, buf []byte, h Handler) ([]byte, []Diag, error) {
	res, err := a.walk(s, op, doc, h, true)
	if err != nil {
		return buf, nil, err
	}

	if res.op == None {
		return buf, a.Diags, nil
	}

	return a.Buffer.Reader().AppendJSON(buf, res), a.Diags, nil
}

func (a *Applier) walk(s *Schema, op Node, doc []byte, h Handler, rewrite bool) (Node, error) {
	a.reset(s, rewrite)

	root, err := a.Buffer.decode(doc)
	if err != nil {
		return Node{}, err
	}

	if op.op == None {
		op = s.root
	}

	res, err := a.apply(s, op, root, h)
	if errors.Is(err, ErrBreak) {
		err = nil
	}

	return res, err
}

func (a *Applier) Rewriting() bool { return a.rewrite }

// Two arenas, walked in parallel but never interlinked — each node's spans
// point only into its own bytes:
//
//	schema (program)  nodes s.prog.code   | bytes s.prog.src         (read-only)
//	data              nodes a.Buffer.code | bytes a.Buffer.src ++ a.Buffer.text
//
// A block payload (off,count) indexes its arena's nodes; a span (off,len) its
// bytes. Bytes slices are read-only, so rewrites that synthesize literals
// (defaults, canon) copy the bytes into the writable text tail and build nodes
// in the data arena (data is where changes live). Data spans resolve a virtual
// src++text concat by off vs len(src), so the input is never copied.

// apply dispatches one node: if h is set the handler sees it first (and may
// rewrite it or recurse via Apply), otherwise the default behaviour runs. h is
// threaded through every recursion so the caller always knows which handler is
// in effect, instead of it being implicit state.
func (a *Applier) apply(s *Schema, op, val Node, h Handler) (Node, error) {
	if h == nil {
		return a.applyStep(s, op, val, h)
	}

	return h(a, s, op, val, h)
}

// Apply runs the default behaviour for a node — the handler's delegate point.
// Its recursions dispatch through h, so pass the handler along (normally the one
// the handler was given) to keep seeing children, or nil to fall to default.
func (a *Applier) Apply(s *Schema, op, val Node, h Handler) (Node, error) {
	return a.applyStep(s, op, val, h)
}

func (a *Applier) applyStep(s *Schema, op, val Node, h Handler) (Node, error) {
	switch op.Op() {
	case Pass:
	case Fail:
		// A forbidden member is reported at its key, as its keyword's finding.
		if n := len(a.Steps); n != 0 && a.Steps[n-1].DataKey.op != None {
			a.Fail(Forbidden, a.Steps[n-1].Op, a.Steps[n-1].DataKey)
		} else {
			a.Fail(Forbidden, op, val)
		}
	case All:
		off, n := op.Off(), op.Arg()

		for i := range n {
			nv, err := a.apply(s, s.prog.code[off+i], val, h)
			if err != nil {
				return nv, err
			}

			val = nv
		}
	case Type:
		a.checkType(op, val)
	case Properties:
		return a.checkProps(s, op, val, h)
	case Required:
		a.checkRequired(s, op, val)
	case MinProps:
		if val.Op() == Object && val.Arg() < op.Imm() {
			a.Fail(TooFewProps, op, val)
		}
	case MaxProps:
		if val.Op() == Object && val.Arg() > op.Imm() {
			a.Fail(TooManyProps, op, val)
		}
	case Prefix:
		return a.checkPrefix(s, op, val, h)
	case Items:
		return a.checkItems(s, op, val, h)
	case MinItems:
		if val.Op() == Array && val.Arg() < op.Imm() {
			a.Fail(TooFewItems, op, val)
		}
	case MaxItems:
		if val.Op() == Array && val.Arg() > op.Imm() {
			a.Fail(TooManyItems, op, val)
		}
	case Unique:
		if op.Imm() != 0 {
			a.checkUnique(op, val)
		}
	case MinLen:
		if val.Op() == String && a.strlen(val) < op.Imm() {
			a.Fail(TooShort, op, val)
		}
	case MaxLen:
		if val.Op() == String && a.strlen(val) > op.Imm() {
			a.Fail(TooLong, op, val)
		}
	case Minimum:
		if isNumber(val) && a.number(val) < a.schemaNum(s, op) {
			a.Fail(BelowMinimum, op, val)
		}
	case Maximum:
		if isNumber(val) && a.number(val) > a.schemaNum(s, op) {
			a.Fail(AboveMaximum, op, val)
		}
	case ExclMin:
		if isNumber(val) && a.number(val) <= a.schemaNum(s, op) {
			a.Fail(BelowMinimumExcl, op, val)
		}
	case ExclMax:
		if isNumber(val) && a.number(val) >= a.schemaNum(s, op) {
			a.Fail(AboveMaximumExcl, op, val)
		}
	case MultipleOf:
		if isNumber(val) && !a.multipleOf(s, op, val) {
			a.Fail(NotMultipleOf, op, val)
		}
	case Enum:
		a.checkEnum(s, op, val)
	case Const:
		if !a.equalLit(s, val, s.prog.code[op.Off()]) {
			a.Fail(MustConst, op, val)
		}
	case Not:
		ok, err := a.matches(s, Step{Op: op, Sub: s.prog.code[op.Off()]}, val, h)
		if err != nil {
			return val, err
		}

		if ok {
			a.Fail(MustNotMatch, op, val)
		}
	case AllOf:
		off, n := op.Off(), op.Arg()

		for i := range n {
			st := Step{Op: op, Value: MakeInt(i), Sub: s.prog.code[off+i]}

			if _, err := a.applyChild(s, st, val, h); err != nil {
				return val, err
			}
		}
	case AnyOf:
		if err := a.checkAnyOf(s, op, val, h); err != nil {
			return val, err
		}
	case OneOf:
		if err := a.checkOneOf(s, op, val, h); err != nil {
			return val, err
		}
	case If:
		return a.checkCond(s, op, val, h)
	case Then, Else:
		// consumed by the sibling If
	case Ref:
		// An external ref lives in another document's program arena; the subtree
		// runs on that document, the data arena stays put.
		ts, tnode, err := s.RefTarget(op)
		if err != nil {
			return val, err
		}

		return a.applyChild(ts, Step{Doc: ts, Op: op, Value: op, Sub: tnode}, val, h)
	case Additional:
		return a.checkAdditional(s, op, val, h)
	case PatternProps:
		return a.checkPatternProps(s, op, val, h)
	case Format:
		if s.Flags.Is(AssertStringFormat) && val.Op() == String &&
			!formatOK(a.Buffer.Reader().Span(val), strFormat(op.Imm()), s.Flags) {
			a.Fail(FormatMismatch, op, val)
		}
	case Pattern:
		if val.Op() == String && !s.patterns[op].Match(a.Buffer.Reader().String(val)) {
			a.Fail(PatternMismatch, op, val)
		}
	case ID, Raw, Ext, Default, Defs:
		// ID only names the schema for $ref to find; Raw/Ext are kept only for
		// round-trip (a Walk handler acts on Ext);
		// Default is consumed by the enclosing Properties (insertion); Defs only
		// holds definitions reached via $ref. Node{} constrains a value here.
	default:
		panic(op.Op())
	}

	return val, nil
}

func (a *Applier) applyChild(s *Schema, st Step, val Node, h Handler) (Node, error) {
	a.push(s, &st)
	defer a.pop()

	return a.apply(s, st.Sub, val, h)
}

func (a *Applier) push(s *Schema, st *Step) {
	if st.Doc == nil {
		st.Doc = s
	}

	if st.DataKey.op != None {
		a.Depth++
	}

	a.Steps = append(a.Steps, *st)
}

func (a *Applier) pop() {
	last := len(a.Steps) - 1

	if a.Steps[last].DataKey.op != None {
		a.Depth--
	}

	a.Steps = a.Steps[:last]
}

func (a *Applier) checkType(op, val Node) {
	mask := TypesOf(op)
	t := dataType(val)

	ok := mask.Any(t)
	if t == TypeNumber && mask.Any(TypeInteger) && a.integral(val) {
		ok = true
	}

	if !ok {
		a.Fail(TypeMismatch, op, val)
	}
}

func (a *Applier) checkProps(s *Schema, op, val Node, h Handler) (Node, error) {
	if val.Op() != Object {
		return val, nil
	}

	if !a.rewrite {
		return val, a.validateProps(s, op, val, h)
	}

	return a.rewriteProps(s, op, val, h)
}

func (a *Applier) validateProps(s *Schema, op, val Node, h Handler) error {
	off, n := op.Off(), op.Arg()

	for i := range n {
		name := s.prog.code[off+2*i]
		sub := s.prog.code[off+2*i+1]

		key, v, ok := a.member(s, val, name)
		if !ok {
			continue
		}

		if _, err := a.applyChild(s, Step{Op: op, Value: name, Sub: sub, DataKey: key}, v, h); err != nil {
			return err
		}
	}

	return nil
}

func (a *Applier) rewriteProps(s *Schema, op, val Node, h Handler) (Node, error) {
	mark := len(a.Buffer.tmp)
	defer func() { a.Buffer.tmp = a.Buffer.tmp[:mark] }()

	var dirty bool
	var err error

	if s.Flags.Is(KeepKeyOrder) {
		dirty, err = a.orderedProps(s, op, val, h)
	} else {
		dirty, err = a.canonProps(s, op, val, h)
	}

	if err != nil {
		return val, err
	}

	if !dirty {
		return val, nil
	}

	return a.Buffer.Writer().Object(a.Buffer.tmp[mark:]...), nil
}

func (a *Applier) orderedProps(s *Schema, op, val Node, h Handler) (bool, error) {
	dirty := false

	voff, vn := val.Off(), val.Arg()

	for i := range vn {
		key := a.Buffer.code[voff+2*i]
		v := a.Buffer.code[voff+2*i+1]

		if name, sub, ok := a.propSub(s, op, key); ok {
			nv, err := a.applyChild(s, Step{Op: op, Value: name, Sub: sub, DataKey: key}, v, h)
			if err != nil {
				return dirty, err
			}

			if nv != v {
				v = nv
				dirty = true
			}
		}

		a.Buffer.tmp = append(a.Buffer.tmp, key, v)
	}

	if s.Flags.Is(KeepMissing) {
		return dirty, nil
	}

	off, n := op.Off(), op.Arg()

	for i := range n {
		name := s.prog.code[off+2*i]

		if _, _, ok := a.member(s, val, name); ok {
			continue
		}

		if dv, ok := a.defaultOf(s, s.prog.code[off+2*i+1]); ok {
			a.Buffer.tmp = append(a.Buffer.tmp, a.copyLit(s, name), a.copyLit(s, dv))
			dirty = true
		}
	}

	return dirty, nil
}

func (a *Applier) canonProps(s *Schema, op, val Node, h Handler) (bool, error) {
	voff, vn := val.Off(), val.Arg()

	dirty := false
	j := int64(0) // source member slot the next emitted pair is compared against

	off, n := op.Off(), op.Arg()

	for i := range n {
		name := s.prog.code[off+2*i]
		sub := s.prog.code[off+2*i+1]

		if key, v, ok := a.member(s, val, name); ok {
			nv, err := a.applyChild(s, Step{Op: op, Value: name, Sub: sub, DataKey: key}, v, h)
			if err != nil {
				return dirty, err
			}

			v = nv

			if key != a.Buffer.code[voff+2*j] || v != a.Buffer.code[voff+2*j+1] {
				dirty = true
			}

			a.Buffer.tmp = append(a.Buffer.tmp, key, v)
			j++
			continue
		}

		if s.Flags.Is(KeepMissing) {
			continue
		}

		if dv, ok := a.defaultOf(s, sub); ok {
			a.Buffer.tmp = append(a.Buffer.tmp, a.copyLit(s, name), a.copyLit(s, dv))
			dirty = true
		}
	}

	for i := range vn {
		key := a.Buffer.code[voff+2*i]
		v := a.Buffer.code[voff+2*i+1]

		if _, _, ok := a.propSub(s, op, key); ok {
			continue
		}

		if key != a.Buffer.code[voff+2*j] || v != a.Buffer.code[voff+2*j+1] {
			dirty = true
		}

		a.Buffer.tmp = append(a.Buffer.tmp, key, v)
		j++
	}

	return dirty, nil
}

func (a *Applier) propSub(s *Schema, op, key Node) (name, sub Node, ok bool) {
	off, n := op.Off(), op.Arg()

	for i := range n {
		if a.keyEq(s, key, s.prog.code[off+2*i]) {
			return s.prog.code[off+2*i], s.prog.code[off+2*i+1], true
		}
	}

	return Node{}, Node{}, false
}

func (a *Applier) defaultOf(s *Schema, sub Node) (Node, bool) {
	if sub.Op() != All {
		return Node{}, false
	}

	for _, ch := range s.prog.Reader().Nodes(sub) {
		if ch.Op() == Default {
			return s.prog.code[ch.Off()], true
		}
	}

	return Node{}, false
}

// copyLit lifts a schema-arena literal (a property name or default value) into
// the data arena.
func (a *Applier) copyLit(s *Schema, op Node) Node {
	return a.Buffer.Writer().CopyFrom(s.prog.Reader(), op)
}

func (a *Applier) checkAdditional(s *Schema, op, val Node, h Handler) (Node, error) {
	if val.Op() != Object {
		return val, nil
	}

	props, patterns, sub := s.prog.Reader().PropertiesParts(op)

	if !a.rewrite {
		return val, a.validateAdditional(s, op, props, patterns, sub, val, h)
	}

	return a.rewriteAdditional(s, op, props, patterns, sub, val, h)
}

func (a *Applier) validateAdditional(s *Schema, op, props, patterns, sub, val Node, h Handler) error {
	voff, vn := val.Off(), val.Arg()

	for i := range vn {
		key := a.Buffer.code[voff+2*i]
		v := a.Buffer.code[voff+2*i+1]

		if a.covered(s, props, patterns, key) {
			continue
		}

		if _, err := a.applyChild(s, Step{Op: op, Sub: sub, DataKey: key}, v, h); err != nil {
			return err
		}
	}

	return nil
}

func (a *Applier) rewriteAdditional(s *Schema, op, props, patterns, sub, val Node, h Handler) (Node, error) {
	mark := len(a.Buffer.tmp)
	defer func() { a.Buffer.tmp = a.Buffer.tmp[:mark] }()

	voff, vn := val.Off(), val.Arg()
	dirty := false

	for i := range vn {
		key := a.Buffer.code[voff+2*i]
		v := a.Buffer.code[voff+2*i+1]

		if !a.covered(s, props, patterns, key) {
			nv, err := a.applyChild(s, Step{Op: op, Sub: sub, DataKey: key}, v, h)
			if err != nil {
				return val, err
			}

			if nv != v {
				v = nv
				dirty = true
			}
		}

		a.Buffer.tmp = append(a.Buffer.tmp, key, v)
	}

	if !dirty {
		return val, nil
	}

	return a.Buffer.Writer().Object(a.Buffer.tmp[mark:]...), nil
}

// covered reports whether key is named in the sibling properties node or matched
// by one of the sibling patternProperties — either way it is not additional.
func (a *Applier) covered(s *Schema, props, patterns, key Node) bool {
	if props.Op() == Properties {
		if _, _, ok := a.propSub(s, props, key); ok {
			return true
		}
	}

	return a.patternHit(s, patterns, key)
}

// patternHit reports whether key matches any regex in a patternProperties node.
func (a *Applier) patternHit(s *Schema, patterns, key Node) bool {
	if patterns.Op() != PatternProps {
		return false
	}

	off, n := patterns.Off(), patterns.Arg()

	for i := range n {
		if s.patterns[s.prog.code[off+2*i]].Match(a.Buffer.Reader().String(key)) {
			return true
		}
	}

	return false
}

func (a *Applier) checkPatternProps(s *Schema, op, val Node, h Handler) (Node, error) {
	if val.Op() != Object {
		return val, nil
	}

	mark := len(a.Buffer.tmp)
	defer func() { a.Buffer.tmp = a.Buffer.tmp[:mark] }()

	off, n := op.Off(), op.Arg()
	voff, vn := val.Off(), val.Arg()
	dirty := false

	for i := range vn {
		key := a.Buffer.code[voff+2*i]
		v := a.Buffer.code[voff+2*i+1]

		for j := range n {
			pat := s.prog.code[off+2*j]
			sub := s.prog.code[off+2*j+1]

			if !s.patterns[pat].Match(a.Buffer.Reader().String(key)) {
				continue
			}

			nv, err := a.applyChild(s, Step{Op: op, Value: pat, Sub: sub, DataKey: key}, v, h)
			if err != nil {
				return val, err
			}

			if nv != v {
				v = nv
				dirty = true
			}
		}

		a.Buffer.tmp = append(a.Buffer.tmp, key, v)
	}

	if !a.rewrite || !dirty {
		return val, nil
	}

	return a.Buffer.Writer().Object(a.Buffer.tmp[mark:]...), nil
}

func (a *Applier) checkRequired(s *Schema, op, val Node) {
	if val.Op() != Object {
		return
	}

	for _, name := range s.prog.Reader().Nodes(op) {
		if _, _, ok := a.member(s, val, name); !ok {
			a.Fail(MissingRequired, name, val)
		}
	}
}

func (a *Applier) checkItems(s *Schema, op, val Node, h Handler) (Node, error) {
	if val.Op() != Array {
		return val, nil
	}

	prefix, sub := s.prog.Reader().ItemsParts(op)

	return a.eachItem(s, op, val, Node{op: Pass}, sub, prefix.ArgInt(), val.ArgInt(), h)
}

func (a *Applier) checkPrefix(s *Schema, op, val Node, h Handler) (Node, error) {
	if val.Op() != Array {
		return val, nil
	}

	return a.eachItem(s, op, val, op, Node{op: Pass}, 0, op.ArgInt(), h)
}

// eachItem applies prefix[i] to item i while i < len(prefix), sub to the rest,
// over the index range [first, last).
func (a *Applier) eachItem(s *Schema, op, val, prefix, sub Node, first, last int, h Handler) (Node, error) {
	mark := len(a.Buffer.tmp)
	defer func() { a.Buffer.tmp = a.Buffer.tmp[:mark] }()

	poff, pn := prefix.OffInt(), prefix.ArgInt()
	voff, vn := val.OffInt(), val.ArgInt()
	dirty := false

	for i := range vn {
		v := a.Buffer.code[voff+i]

		sch := sub
		if i < pn {
			sch = s.prog.code[poff+i]
		}

		if i < first || i >= last {
			a.Buffer.tmp = append(a.Buffer.tmp, v)
			continue
		}

		kw, idx := op, Node{}
		if i < pn {
			kw, idx = prefix, MakeInt(int64(i))
		}

		nv, err := a.applyChild(s, Step{Op: kw, Value: idx, Sub: sch, DataKey: MakeInt(int64(i))}, v, h)
		if err != nil {
			return val, err
		}

		if nv != v {
			dirty = true
		}

		a.Buffer.tmp = append(a.Buffer.tmp, nv)
	}

	if !dirty {
		return val, nil
	}

	return a.Buffer.Writer().Array(a.Buffer.tmp[mark:]...), nil
}

func (a *Applier) checkUnique(op, val Node) {
	if val.Op() != Array {
		return
	}

	voff, vn := val.Off(), val.Arg()

	for i := range vn {
		for j := i + 1; j < vn; j++ {
			if equalBuf(a.Buffer.Reader(), a.Buffer.code[voff+i], a.Buffer.Reader(), a.Buffer.code[voff+j]) {
				a.Fail(DuplicateItems, op, a.Buffer.code[voff+j])
				return
			}
		}
	}
}

func (a *Applier) checkEnum(s *Schema, op, val Node) {
	off, n := op.Off(), op.Arg()

	for i := range n {
		if a.equalLit(s, val, s.prog.code[off+i]) {
			return
		}
	}

	a.Fail(MustMatchEnum, op, val)
}

func (a *Applier) checkAnyOf(s *Schema, op, val Node, h Handler) error {
	off, n := op.Off(), op.Arg()

	for i := range n {
		ok, err := a.matches(s, Step{Op: op, Value: MakeInt(i), Sub: s.prog.code[off+i]}, val, h)
		if err != nil {
			return err
		}

		if ok {
			return nil
		}
	}

	a.Fail(MustMatchAny, op, val)

	return nil
}

func (a *Applier) checkOneOf(s *Schema, op, val Node, h Handler) error {
	off, n := op.Off(), op.Arg()
	cnt := 0

	for i := range n {
		ok, err := a.matches(s, Step{Op: op, Value: MakeInt(i), Sub: s.prog.code[off+i]}, val, h)
		if err != nil {
			return err
		}

		if ok {
			cnt++
		}
	}

	if cnt == 0 {
		a.Fail(MustMatchOne, op, val)
	} else if cnt > 1 {
		a.Fail(MustMatchOnlyOne, op, val)
	}

	return nil
}

func (a *Applier) checkCond(s *Schema, op, val Node, h Handler) (Node, error) {
	cond, then, els := s.prog.Reader().CondParts(op)

	ok, err := a.matches(s, Step{Op: op, Value: Node{op: If}, Sub: cond}, val, h)
	if err != nil {
		return val, err
	}

	if ok {
		return a.applyChild(s, Step{Op: op, Value: Node{op: Then}, Sub: then}, val, h)
	}

	return a.applyChild(s, Step{Op: op, Value: Node{op: Else}, Sub: els}, val, h)
}

// matches calls apply, but drops diag messages.
// matches runs a trial branch: it reports whether the subschema held and drops
// whatever it had to say either way. A caller that wants those diagnostics reads
// them as they appear — through a handler — because by the time this returns they
// are gone.
func (a *Applier) matches(s *Schema, st Step, val Node, h Handler) (bool, error) {
	n := len(a.Diags)
	defer func() { a.Diags = a.Diags[:n] }()

	if _, err := a.applyChild(s, st, val, h); err != nil {
		return false, err
	}

	return len(a.Diags) == n, nil
}

func (a *Applier) member(s *Schema, obj, key Node) (k, v Node, ok bool) {
	voff, vn := obj.Off(), obj.Arg()

	for i := range vn {
		if a.keyEq(s, a.Buffer.code[voff+2*i], key) {
			return a.Buffer.code[voff+2*i], a.Buffer.code[voff+2*i+1], true
		}
	}

	return Node{}, Node{}, false
}

func (a *Applier) keyEq(s *Schema, data, schema Node) bool {
	return bytes.Equal(a.Buffer.Reader().Span(data), s.prog.Reader().Span(schema))
}

func (a *Applier) equalLit(s *Schema, val, lit Node) bool {
	return equalBuf(a.Buffer.Reader(), val, s.prog.Reader(), lit)
}

func (a *Applier) number(val Node) float64 {
	v, _ := a.Buffer.Reader().Float(val)
	return v
}

func (a *Applier) schemaNum(s *Schema, op Node) float64 {
	lit := s.prog.code[op.Off()]
	v, _ := json2.Value(s.prog.Reader().Span(lit)).Float64()
	return v
}

func (a *Applier) multipleOf(s *Schema, op, val Node) bool {
	lit := s.prog.code[op.Off()]
	div := s.prog.Reader().Span(lit)

	var ok, exact bool

	switch val.Op() {
	case Number:
		ok, exact = isMultiple(a.Buffer.Reader().Span(val), div)
	case IntLit:
		if md, sd, good := parseDecimal(div); good {
			ok, exact = isMultipleDec(magnitude(val.Int()), 0, md, sd)
		}
	}

	if exact {
		return ok
	}

	m := a.schemaNum(s, op)
	return m == 0 || math.Mod(a.number(val), m) == 0
}

func (a *Applier) integral(val Node) bool {
	v := a.number(val)
	return v == math.Trunc(v)
}

func (a *Applier) strlen(val Node) int64 {
	return int64(utf8.RuneCount(a.Buffer.Reader().Span(val)))
}

func (a *Applier) Fail(code DiagCode, op, val Node) {
	d := Diag{Code: code, Op: op, Val: val}

	if a.save {
		d.Steps = append([]Step(nil), a.Steps...)
	}

	a.Diags = append(a.Diags, d)
}

func (a *Applier) reset(s *Schema, rewrite bool) {
	a.Buffer.Reset()

	if a.Diags == nil {
		a.Diags = a.dbuf[:]
	}

	a.rewrite = rewrite
	a.save = s.Flags.Is(SaveSteps)
	a.Diags = a.Diags[:0]
	a.Steps = a.Steps[:0]
	a.Depth = 0
}

// magnitude is |v| as unsigned, exact for math.MinInt64 too.
func magnitude(v int64) uint64 {
	u := uint64(v)
	if v < 0 {
		u = -u
	}

	return u
}

func isNumber(op Node) bool {
	switch op.Op() {
	case Number, IntLit, FltLit:
		return true
	default:
		return false
	}
}

func dataType(val Node) Types {
	switch val.Op() {
	case Null:
		return TypeNull
	case True, False:
		return TypeBoolean
	case Number, IntLit, FltLit:
		return TypeNumber
	case String:
		return TypeString
	case Array:
		return TypeArray
	case Object:
		return TypeObject
	default:
		return 0
	}
}

func equalBuf(lb BufferReader, l Node, rb BufferReader, r Node) bool {
	// Numbers compare by value across shapes: the same number reaches here as a
	// Number span from the input or as an IntLit/FltLit word from a handler.
	if isNumber(l) && isNumber(r) {
		lv, _ := lb.Float(l)
		rv, _ := rb.Float(r)

		return lv == rv
	}

	if l.Op() != r.Op() {
		return false
	}

	switch l.Op() {
	case Null, True, False:
		return true
	case String:
		return bytes.Equal(lb.Span(l), rb.Span(r))
	case Array:
		lo, ln := l.Off(), l.Arg()
		ro, rn := r.Off(), r.Arg()

		if ln != rn {
			return false
		}

		for i := range ln {
			if !equalBuf(lb, lb.code[lo+i], rb, rb.code[ro+i]) {
				return false
			}
		}

		return true
	case Object:
		lo, ln := l.Off(), l.Arg()
		ro, rn := r.Off(), r.Arg()

		if ln != rn {
			return false
		}

		for i := range ln {
			lk := lb.code[lo+2*i]
			lv := lb.code[lo+2*i+1]

			if objCount(lb, lo, ln, lb, lk, lv) != objCount(rb, ro, rn, lb, lk, lv) {
				return false
			}
		}

		return true
	default:
		return false
	}
}

func objCount(hb BufferReader, off, n int64, kb BufferReader, key, val Node) (c int64) {
	for j := range n {
		if equalBuf(hb, hb.code[off+2*j], kb, key) && equalBuf(hb, hb.code[off+2*j+1], kb, val) {
			c++
		}
	}

	return c
}
