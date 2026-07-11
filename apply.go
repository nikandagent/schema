package schema

import (
	"bytes"
	"errors"
	"math"

	"nikand.dev/go/json2"
)

type (
	Applier struct {
		s *Schema
		b Buffer

		diag []Diag

		spath []Opcode
		dpath []Opcode

		at []Opcode

		opbuf [23]Opcode // spath[:8] | dpath[8:16] | at[16:23]; sized so Applier fills the 896 bucket
		dbuf  [3]Diag

		rewrite bool
	}

	// Handler is called per node during a Walk. It receives the handler to
	// delegate with (normally itself, so children reach the handler too) and
	// passes it on to Apply — pass nil to run a subtree with default behaviour
	// only, or a different Handler to swap behaviour for that subtree.
	Handler func(c *Applier, op, val Opcode, h Handler) (Opcode, error)
)

// ErrBreak is returned by a Handler to stop the walk cleanly.
var ErrBreak = errors.New("break")

func (s *Schema) Validate(doc []byte, opts ...Option) ([]Diag, error) {
	return s.Walk(doc, nil, opts...)
}

func (s *Schema) Rewrite(w, doc []byte, opts ...Option) ([]byte, []Diag, error) {
	return s.WalkRewrite(w, doc, nil, opts...)
}

func (s *Schema) Walk(doc []byte, h Handler, opts ...Option) ([]Diag, error) {
	_, a, err := s.walk(doc, h, false, opts...)
	if err != nil {
		return nil, err
	}

	return a.diag, err
}

func (s *Schema) WalkRewrite(w, doc []byte, h Handler, opts ...Option) ([]byte, []Diag, error) {
	res, a, err := s.walk(doc, h, true, opts...)
	if err != nil {
		return w, nil, err
	}
	if res == None {
		return w, a.diag, nil
	}

	return a.b.Reader().AppendJSON(w, res), a.diag, nil
}

func (s *Schema) walk(doc []byte, h Handler, rewrite bool, opts ...Option) (_ Opcode, a *Applier, err error) {
	for _, o := range opts {
		if u, ok := o.(use); ok {
			a = u.a
		}
	}

	if a == nil {
		a = &s.c
	}

	a.reset(s, rewrite)

	root, err := a.b.decode(doc)
	if err != nil {
		return None, nil, err
	}

	for _, o := range opts {
		err := o.apply(a)
		if err != nil {
			return None, nil, err
		}
	}

	res, err := a.apply(s.root, root, h)
	if errors.Is(err, ErrBreak) {
		err = nil
	}

	return res, a, err
}

func (a *Applier) Buf() *Buffer            { return &a.b }
func (a *Applier) SchemaBuf() BufferReader { return a.s.prog.Reader() }
func (a *Applier) Rewriting() bool         { return a.rewrite }

// Diags is the accumulated diagnostics so far; its length marks the point before
// a subtree ran. SetDiags writes back a filtered slice — snapshot len(Diags()),
// recurse via Apply, then drop the tail's unwanted entries. See matches, which
// uses the same snapshot/rewind internally to discard a trial branch's diags.
func (a *Applier) Diags() []Diag     { return a.diag }
func (a *Applier) SetDiags(d []Diag) { a.diag = d }

func (a *Applier) DataPath() []Opcode   { return a.dpath }
func (a *Applier) SchemaPath() []Opcode { return a.spath }

// Two arenas, walked in parallel but never interlinked — each node's spans
// point only into its own bytes:
//
//	schema (program)  nodes s.prog.code | bytes s.prog.src           (read-only)
//	data              nodes c.b.code    | bytes c.b.src ++ c.b.text
//
// A block payload (off,count) indexes its arena's nodes; a span (off,len) its
// bytes. Bytes slices are read-only, so rewrites that synthesize literals
// (defaults, canon) copy the bytes into the writable c.b.text tail and build
// nodes in c.b.code (data is where changes live). Data spans resolve a virtual
// src++text concat by off vs len(src), so the input is never copied.

// apply dispatches one node: if h is set the handler sees it first (and may
// rewrite it or recurse via Apply), otherwise the default behaviour runs. h is
// threaded through every recursion so the caller always knows which handler is
// in effect, instead of it being implicit state.
func (a *Applier) apply(op, val Opcode, h Handler) (Opcode, error) {
	if h == nil {
		return a.applyStep(op, val, h)
	}

	return h(a, op, val, h)
}

// Apply runs the default behaviour for a node — the handler's delegate point.
// Its recursions dispatch through h, so pass the handler along (normally the one
// the handler was given) to keep seeing children, or nil to fall to default.
func (a *Applier) Apply(op, val Opcode, h Handler) (Opcode, error) {
	return a.applyStep(op, val, h)
}

func (a *Applier) applyStep(op, val Opcode, h Handler) (Opcode, error) {
	switch op.Op() {
	case Pass:
	case Fail:
		a.Fail(op, val, "schema forbids any value")
	case All:
		off, n := op.Off(), op.Arg()

		for i := range n {
			nv, err := a.apply(a.s.prog.code[off+i], val, h)
			if err != nil {
				return nv, err
			}

			val = nv
		}
	case Type:
		a.checkType(op, val)
	case Properties:
		return a.checkProps(op, val, h)
	case Required:
		a.checkRequired(op, val)
	case MinProps:
		if val.Op() == Object && val.Arg() < op.Imm() {
			a.Fail(op, val, "too few properties")
		}
	case MaxProps:
		if val.Op() == Object && val.Arg() > op.Imm() {
			a.Fail(op, val, "too many properties")
		}
	case Items:
		return a.checkItems(op, val, h)
	case MinItems:
		if val.Op() == Array && val.Arg() < op.Imm() {
			a.Fail(op, val, "too few items")
		}
	case MaxItems:
		if val.Op() == Array && val.Arg() > op.Imm() {
			a.Fail(op, val, "too many items")
		}
	case Unique:
		a.checkUnique(op, val)
	case MinLen:
		if val.Op() == Str && a.strlen(val) < op.Imm() {
			a.Fail(op, val, "too short")
		}
	case MaxLen:
		if val.Op() == Str && a.strlen(val) > op.Imm() {
			a.Fail(op, val, "too long")
		}
	case Minimum:
		if val.Op() == Num && a.number(val) < a.schemaNum(op) {
			a.Fail(op, val, "less than minimum")
		}
	case Maximum:
		if val.Op() == Num && a.number(val) > a.schemaNum(op) {
			a.Fail(op, val, "greater than maximum")
		}
	case ExclMin:
		if val.Op() == Num && a.number(val) <= a.schemaNum(op) {
			a.Fail(op, val, "not above exclusive minimum")
		}
	case ExclMax:
		if val.Op() == Num && a.number(val) >= a.schemaNum(op) {
			a.Fail(op, val, "not below exclusive maximum")
		}
	case MultipleOf:
		if val.Op() == Num && !a.multipleOf(op, val) {
			a.Fail(op, val, "not a multiple")
		}
	case Enum:
		a.checkEnum(op, val)
	case Const:
		if !a.equalLit(val, a.s.prog.code[op.Off()]) {
			a.Fail(op, val, "not the const value")
		}
	case Not:
		ok, err := a.matches(a.s.prog.code[op.Off()], val, h)
		if err != nil {
			return val, err
		}

		if ok {
			a.Fail(op, val, "matches a forbidden schema")
		}
	case AllOf:
		off, n := op.Off(), op.Arg()

		for i := range n {
			if _, err := a.apply(a.s.prog.code[off+i], val, h); err != nil {
				return val, err
			}
		}
	case AnyOf:
		if err := a.checkAnyOf(op, val, h); err != nil {
			return val, err
		}
	case OneOf:
		if err := a.checkOneOf(op, val, h); err != nil {
			return val, err
		}
	case Ref:
		// An external ref lives in another document's program arena; swap it in for
		// the subtree (the data arena c.b stays put), then restore.
		ts, tnode, err := a.s.refResolve(op)
		if err != nil {
			return val, err
		}

		if ts == a.s {
			return a.apply(tnode, val, h)
		}

		defer func(s *Schema) { a.s = s }(a.s)
		a.s = ts

		return a.apply(tnode, val, h)
	case Additional:
		return a.checkAdditional(op, val, h)
	case PatternProps:
		return a.checkPatternProps(op, val, h)
	case Pattern:
		if val.Op() == Str && !a.s.patterns[op].Match(a.b.Reader().String(val)) {
			a.Fail(op, val, "does not match pattern")
		}
	case Raw, Ext, Default, Defs:
		// Raw/Ext are kept only for round-trip (a Walk handler acts on Ext);
		// Default is consumed by the enclosing Properties (insertion); Defs only
		// holds definitions reached via $ref. None constrains a value here.
	default:
		panic(op)
	}

	return val, nil
}

func (a *Applier) applyChild(sub, val, step Opcode, h Handler) (Opcode, error) {
	mark := len(a.spath)

	a.spath = append(a.spath, sub)
	a.dpath = append(a.dpath, step)

	defer func() {
		a.spath = a.spath[:mark]
		a.dpath = a.dpath[:mark]
	}()

	return a.apply(sub, val, h)
}

func (a *Applier) checkType(op, val Opcode) {
	mask := op.ImmInt()
	t := dataType(val)

	ok := mask&t != 0
	if t == typeNum && mask&typeInt != 0 && a.integral(val) {
		ok = true
	}

	if !ok {
		a.Fail(op, val, "wrong type")
	}
}

func (a *Applier) checkProps(op, val Opcode, h Handler) (Opcode, error) {
	seek := a.seeking()

	switch seek.Op() {
	case None, Key, Each:
		// ok
	case IntLit:
		a.Fail(op, None, "schema is object, val supposes array")
		return val, nil
	default:
		panic(seek)
	}

	if val.Op() != Object {
		return val, nil
	}

	if !a.rewrite {
		return val, a.validateProps(op, val, h)
	}

	return a.rewriteProps(op, val, h)
}

func (a *Applier) validateProps(op, val Opcode, h Handler) error {
	seek := a.seeking()
	off, n := op.Off(), op.Arg()

	for i := range n {
		name := a.s.prog.code[off+2*i]
		sub := a.s.prog.code[off+2*i+1]

		if seek.Op() == Key && !a.idEq(seek, name) {
			continue
		}

		key, v, ok := a.member(val, name)
		if !ok {
			continue
		}

		if _, err := a.applyChild(sub, v, key, h); err != nil {
			return err
		}
	}

	return nil
}

func (a *Applier) rewriteProps(op, val Opcode, h Handler) (Opcode, error) {
	mark := len(a.b.tmp)
	defer func() { a.b.tmp = a.b.tmp[:mark] }()

	var dirty bool
	var err error

	if a.s.Flags.Is(KeepKeyOrder) {
		dirty, err = a.orderedProps(op, val, h)
	} else {
		dirty, err = a.canonProps(op, val, h)
	}

	if err != nil {
		return val, err
	}

	if !dirty {
		return val, nil
	}

	return a.b.Writer().Object(a.b.tmp[mark:]...), nil
}

func (a *Applier) orderedProps(op, val Opcode, h Handler) (bool, error) {
	dirty := false

	voff, vn := val.Off(), val.Arg()

	for i := range vn {
		key := a.b.code[voff+2*i]
		v := a.b.code[voff+2*i+1]

		if sub, ok := a.propSub(op, key); ok {
			nv, err := a.applyChild(sub, v, key, h)
			if err != nil {
				return dirty, err
			}

			if nv != v {
				v = nv
				dirty = true
			}
		}

		a.b.tmp = append(a.b.tmp, key, v)
	}

	if a.s.Flags.Is(KeepMissing) {
		return dirty, nil
	}

	off, n := op.Off(), op.Arg()

	for i := range n {
		name := a.s.prog.code[off+2*i]

		if _, _, ok := a.member(val, name); ok {
			continue
		}

		if dv, ok := a.defaultOf(a.s.prog.code[off+2*i+1]); ok {
			a.b.tmp = append(a.b.tmp, a.copyLit(name), a.copyLit(dv))
			dirty = true
		}
	}

	return dirty, nil
}

func (a *Applier) canonProps(op, val Opcode, h Handler) (bool, error) {
	voff, vn := val.Off(), val.Arg()

	dirty := false
	j := int64(0) // source member slot the next emitted pair is compared against

	off, n := op.Off(), op.Arg()

	for i := range n {
		name := a.s.prog.code[off+2*i]
		sub := a.s.prog.code[off+2*i+1]

		if key, v, ok := a.member(val, name); ok {
			nv, err := a.applyChild(sub, v, key, h)
			if err != nil {
				return dirty, err
			}

			v = nv

			if key != a.b.code[voff+2*j] || v != a.b.code[voff+2*j+1] {
				dirty = true
			}

			a.b.tmp = append(a.b.tmp, key, v)
			j++
			continue
		}

		if a.s.Flags.Is(KeepMissing) {
			continue
		}

		if dv, ok := a.defaultOf(sub); ok {
			a.b.tmp = append(a.b.tmp, a.copyLit(name), a.copyLit(dv))
			dirty = true
		}
	}

	for i := range vn {
		key := a.b.code[voff+2*i]
		v := a.b.code[voff+2*i+1]

		if _, ok := a.propSub(op, key); ok {
			continue
		}

		if key != a.b.code[voff+2*j] || v != a.b.code[voff+2*j+1] {
			dirty = true
		}

		a.b.tmp = append(a.b.tmp, key, v)
		j++
	}

	return dirty, nil
}

func (a *Applier) propSub(op, key Opcode) (Opcode, bool) {
	off, n := op.Off(), op.Arg()

	for i := range n {
		if a.keyEq(key, a.s.prog.code[off+2*i]) {
			return a.s.prog.code[off+2*i+1], true
		}
	}

	return 0, false
}

func (a *Applier) defaultOf(sub Opcode) (Opcode, bool) {
	if sub.Op() != All {
		return 0, false
	}

	for _, ch := range a.s.prog.Reader().Nodes(sub) {
		if ch.Op() == Default {
			return a.s.prog.code[ch.Off()], true
		}
	}

	return 0, false
}

// copyLit lifts a schema-arena literal (a property name or default value) into
// the data arena.
func (a *Applier) copyLit(op Opcode) Opcode {
	return a.b.Writer().CopyFrom(a.s.prog.Reader(), op)
}

func (a *Applier) checkAdditional(op, val Opcode, h Handler) (Opcode, error) {
	if val.Op() != Object {
		return val, nil
	}

	props, patterns, sub := a.s.additionalParts(op)

	if !a.rewrite {
		return val, a.validateAdditional(props, patterns, sub, val, h)
	}

	return a.rewriteAdditional(props, patterns, sub, val, h)
}

func (a *Applier) validateAdditional(props, patterns, sub, val Opcode, h Handler) error {
	voff, vn := val.Off(), val.Arg()

	for i := range vn {
		key := a.b.code[voff+2*i]
		v := a.b.code[voff+2*i+1]

		if a.covered(props, patterns, key) {
			continue
		}

		if !a.memberSought(key) {
			continue
		}

		if _, err := a.applyChild(sub, v, key, h); err != nil {
			return err
		}
	}

	return nil
}

func (a *Applier) rewriteAdditional(props, patterns, sub, val Opcode, h Handler) (Opcode, error) {
	mark := len(a.b.tmp)
	defer func() { a.b.tmp = a.b.tmp[:mark] }()

	voff, vn := val.Off(), val.Arg()
	dirty := false

	for i := range vn {
		key := a.b.code[voff+2*i]
		v := a.b.code[voff+2*i+1]

		if !a.covered(props, patterns, key) {
			nv, err := a.applyChild(sub, v, key, h)
			if err != nil {
				return val, err
			}

			if nv != v {
				v = nv
				dirty = true
			}
		}

		a.b.tmp = append(a.b.tmp, key, v)
	}

	if !dirty {
		return val, nil
	}

	return a.b.Writer().Object(a.b.tmp[mark:]...), nil
}

// covered reports whether key is named in the sibling properties node or matched
// by one of the sibling patternProperties — either way it is not additional.
func (a *Applier) covered(props, patterns, key Opcode) bool {
	if props.Op() == Properties {
		if _, ok := a.propSub(props, key); ok {
			return true
		}
	}

	return a.patternHit(patterns, key)
}

// patternHit reports whether key matches any regex in a patternProperties node.
func (a *Applier) patternHit(patterns, key Opcode) bool {
	if patterns.Op() != PatternProps {
		return false
	}

	off, n := patterns.Off(), patterns.Arg()

	for i := range n {
		if a.s.patterns[a.s.prog.code[off+2*i]].Match(a.b.Reader().String(key)) {
			return true
		}
	}

	return false
}

func (a *Applier) checkPatternProps(op, val Opcode, h Handler) (Opcode, error) {
	if val.Op() != Object {
		return val, nil
	}

	mark := len(a.b.tmp)
	defer func() { a.b.tmp = a.b.tmp[:mark] }()

	off, n := op.Off(), op.Arg()
	voff, vn := val.Off(), val.Arg()
	dirty := false

	for i := range vn {
		key := a.b.code[voff+2*i]
		v := a.b.code[voff+2*i+1]

		if !a.memberSought(key) {
			a.b.tmp = append(a.b.tmp, key, v)
			continue
		}

		for j := range n {
			pat := a.s.prog.code[off+2*j]
			sub := a.s.prog.code[off+2*j+1]

			if !a.s.patterns[pat].Match(a.b.Reader().String(key)) {
				continue
			}

			nv, err := a.applyChild(sub, v, key, h)
			if err != nil {
				return val, err
			}

			if nv != v {
				v = nv
				dirty = true
			}
		}

		a.b.tmp = append(a.b.tmp, key, v)
	}

	if !a.rewrite || !dirty {
		return val, nil
	}

	return a.b.Writer().Object(a.b.tmp[mark:]...), nil
}

func (a *Applier) checkRequired(op, val Opcode) {
	if a.seeking() != None {
		return
	}

	if val.Op() != Object {
		return
	}

	off, n := op.Off(), op.Arg()

	for i := range n {
		if _, _, ok := a.member(val, a.s.prog.code[off+i]); !ok {
			a.Fail(op, val, "missing required property")
		}
	}
}

func (a *Applier) checkItems(op, val Opcode, h Handler) (Opcode, error) {
	if val.Op() != Array {
		return val, nil
	}

	seek := a.seeking()

	switch seek.Op() {
	case None, Each, IntLit:
		// ok
	case Key:
		a.Fail(op, None, "schema is array, val supposes object")
		return val, nil
	default:
		panic(seek)
	}

	mark := len(a.b.tmp)
	defer func() { a.b.tmp = a.b.tmp[:mark] }()

	sub := a.s.prog.code[op.Off()]
	voff, vn := val.Off(), val.Arg()
	dirty := false

	target := seek.Imm()
	if target < 0 {
		target += vn
	}

	for i := range vn {
		if seek.Op() == IntLit && i != target {
			continue
		}

		v := a.b.code[voff+i]

		nv, err := a.applyChild(sub, v, makeImm(IntLit, int(i)), h)
		if err != nil {
			return val, err
		}

		if nv != v {
			dirty = true
		}

		a.b.tmp = append(a.b.tmp, nv)
	}

	if !dirty {
		return val, nil
	}

	return a.b.Writer().Array(a.b.tmp[mark:]...), nil
}

func (a *Applier) checkUnique(op, val Opcode) {
	if val.Op() != Array {
		return
	}

	voff, vn := val.Off(), val.Arg()

	for i := range vn {
		for j := i + 1; j < vn; j++ {
			if equalBuf(a.b.Reader(), a.b.code[voff+i], a.b.Reader(), a.b.code[voff+j]) {
				a.Fail(op, val, "duplicate items")
				return
			}
		}
	}
}

func (a *Applier) checkEnum(op, val Opcode) {
	off, n := op.Off(), op.Arg()

	for i := range n {
		if a.equalLit(val, a.s.prog.code[off+i]) {
			return
		}
	}

	a.Fail(op, val, "not in enum")
}

func (a *Applier) checkAnyOf(op, val Opcode, h Handler) error {
	off, n := op.Off(), op.Arg()

	for i := range n {
		ok, err := a.matches(a.s.prog.code[off+i], val, h)
		if err != nil {
			return err
		}

		if ok {
			return nil
		}
	}

	a.Fail(op, val, "matches none of the schemas")

	return nil
}

func (a *Applier) checkOneOf(op, val Opcode, h Handler) error {
	off, n := op.Off(), op.Arg()
	cnt := 0

	for i := range n {
		ok, err := a.matches(a.s.prog.code[off+i], val, h)
		if err != nil {
			return err
		}

		if ok {
			cnt++
		}
	}

	if cnt != 1 {
		a.Fail(op, val, "must match exactly one schema")
	}

	return nil
}

// matches calls apply, but drops diag messages.
func (a *Applier) matches(op, val Opcode, h Handler) (bool, error) {
	n := len(a.diag)
	defer func() { a.diag = a.diag[:n] }()

	if _, err := a.apply(op, val, h); err != nil {
		return false, err
	}

	return len(a.diag) == n, nil
}

func (a *Applier) member(obj, key Opcode) (k, v Opcode, ok bool) {
	voff, vn := obj.Off(), obj.Arg()

	for i := range vn {
		if a.keyEq(a.b.code[voff+2*i], key) {
			return a.b.code[voff+2*i], a.b.code[voff+2*i+1], true
		}
	}

	return 0, 0, false
}

func (a *Applier) keyEq(data, schema Opcode) bool {
	return bytes.Equal(a.b.Reader().Span(data), a.s.prog.Reader().Span(schema))
}

func (a *Applier) idEq(id, schema Opcode) bool {
	return bytes.Equal(a.b.Reader().Span(id), a.s.prog.Reader().String(schema))
}

func (a *Applier) memberSought(key Opcode) bool {
	seek := a.seeking()

	switch seek.Op() {
	case None, Each:
		return true
	case Key:
		return bytes.Equal(a.b.Reader().Span(seek), a.b.Reader().String(key))
	default:
		return false
	}
}

func (a *Applier) equalLit(val, lit Opcode) bool {
	return equalBuf(a.b.Reader(), val, a.s.prog.Reader(), lit)
}

func (a *Applier) number(val Opcode) float64 {
	v, _ := json2.Value(a.b.Reader().Span(val)).Float64()
	return v
}

func (a *Applier) schemaNum(op Opcode) float64 {
	lit := a.s.prog.code[op.Off()]
	v, _ := json2.Value(a.s.prog.Reader().Span(lit)).Float64()
	return v
}

func (a *Applier) multipleOf(op, val Opcode) bool {
	lit := a.s.prog.code[op.Off()]

	ok, exact := isMultiple(a.b.Reader().Span(val), a.s.prog.Reader().Span(lit))
	if exact {
		return ok
	}

	m := a.schemaNum(op)
	return m == 0 || math.Mod(a.number(val), m) == 0
}

func (a *Applier) integral(val Opcode) bool {
	v := a.number(val)
	return v == math.Trunc(v)
}

func (a *Applier) strlen(val Opcode) int64 {
	var d json2.Iterator

	_, rs, _, _ := d.DecodedStringLength(a.b.src, val.OffInt())
	return int64(rs)
}

func (a *Applier) Fail(op, val Opcode, msg string) {
	off, end := a.b.Reader().span(val)
	a.diag = append(a.diag, Diag{Off: off, End: end, Op: op.Op(), Message: msg})
}

func (a *Applier) seeking() Opcode {
	if len(a.dpath) < len(a.at) {
		return a.at[len(a.dpath)]
	}

	return None
}

func (a *Applier) reset(s *Schema, rewrite bool) *Applier {
	a.s = s
	a.b.Reset()

	if a.diag == nil {
		a.diag = a.dbuf[:]
	}
	if a.spath == nil {
		a.spath = a.opbuf[:8:8]
	}
	if a.dpath == nil {
		a.dpath = a.opbuf[8:16:16]
	}
	if a.at == nil {
		a.at = a.opbuf[16:]
	}

	a.rewrite = rewrite
	a.diag = a.diag[:0]
	a.spath = a.spath[:0]
	a.dpath = a.dpath[:0]
	a.at = a.at[:0]

	return a
}

func dataType(val Opcode) int {
	switch val.Op() {
	case Null:
		return typeNull
	case True, False:
		return typeBool
	case Num:
		return typeNum
	case Str:
		return typeStr
	case Array:
		return typeArr
	case Object:
		return typeObj
	default:
		return 0
	}
}

func equalBuf(lb BufferReader, l Opcode, rb BufferReader, r Opcode) bool {
	if l.Op() != r.Op() {
		return false
	}

	switch l.Op() {
	case Null, True, False:
		return true
	case Num:
		lv, _ := json2.Value(lb.Span(l)).Float64()
		rv, _ := json2.Value(rb.Span(r)).Float64()

		return lv == rv
	case Str:
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

func objCount(hb BufferReader, off, n int64, kb BufferReader, key, val Opcode) (c int64) {
	for j := range n {
		if equalBuf(hb, hb.code[off+2*j], kb, key) && equalBuf(hb, hb.code[off+2*j+1], kb, val) {
			c++
		}
	}

	return c
}
