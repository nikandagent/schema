package schema

import (
	"bytes"
	"errors"
	"math"

	"nikand.dev/go/json2"
)

type (
	cur struct {
		s *Schema
		b *Buffer

		rewrite bool
		diag    []Diag

		spath []Opcode
		dpath []Opcode

		at []Opcode
	}

	// Handler is called per node during a Walk. It receives the handler to
	// delegate with (normally itself, so children reach the handler too) and
	// passes it on to Apply — pass nil to run a subtree with default behaviour
	// only, or a different Handler to swap behaviour for that subtree.
	Handler func(c Applier, op, val Opcode, h Handler) (Opcode, error)

	Applier interface {
		Apply(op, val Opcode, h Handler) (Opcode, error)
		Fail(op, val Opcode, msg string)
		Buf() *Buffer
		SchemaBuf() BufferReader
		Rewriting() bool

		Diags() []Diag
		SetDiags(d []Diag)

		DataPath() []Opcode
		SchemaPath() []Opcode
	}
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
	_, diag, err := s.walk(doc, h, false, opts...)

	return diag, err
}

func (s *Schema) WalkRewrite(w, doc []byte, h Handler, opts ...Option) ([]byte, []Diag, error) {
	res, diag, err := s.walk(doc, h, true, opts...)
	if err != nil {
		return w, diag, err
	}
	if res == None {
		return w, diag, nil
	}

	return s.b.Reader().AppendJSON(w, res), diag, nil
}

func (s *Schema) walk(doc []byte, h Handler, rewrite bool, opts ...Option) (Opcode, []Diag, error) {
	s.b.Reset()

	c := cur{
		s:       s,
		b:       &s.b,
		rewrite: rewrite,
	}

	root, err := c.b.decode(doc)
	if err != nil {
		return None, nil, err
	}

	for _, o := range opts {
		err := o(&c)
		if err != nil {
			return None, nil, err
		}
	}

	res, err := c.apply(s.root, root, h)

	return res, c.diag, c.result(err)
}

func (c *cur) Buf() *Buffer            { return c.b }
func (c *cur) SchemaBuf() BufferReader { return c.s.prog.Reader() }
func (c *cur) Rewriting() bool         { return c.rewrite }

// Diags is the accumulated diagnostics so far; its length marks the point before
// a subtree ran. SetDiags writes back a filtered slice — snapshot len(Diags()),
// recurse via Apply, then drop the tail's unwanted entries. See matches, which
// uses the same snapshot/rewind internally to discard a trial branch's diags.
func (c *cur) Diags() []Diag     { return c.diag }
func (c *cur) SetDiags(d []Diag) { c.diag = d }

func (c *cur) DataPath() []Opcode   { return c.dpath }
func (c *cur) SchemaPath() []Opcode { return c.spath }

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
func (c *cur) apply(op, val Opcode, h Handler) (Opcode, error) {
	if h == nil {
		return c.applyStep(op, val, h)
	}

	return h(c, op, val, h)
}

// Apply runs the default behaviour for a node — the handler's delegate point.
// Its recursions dispatch through h, so pass the handler along (normally the one
// the handler was given) to keep seeing children, or nil to fall to default.
func (c *cur) Apply(op, val Opcode, h Handler) (Opcode, error) {
	return c.applyStep(op, val, h)
}

func (c *cur) applyStep(op, val Opcode, h Handler) (Opcode, error) {
	switch op.Op() {
	case Pass:
	case Fail:
		c.Fail(op, val, "schema forbids any value")
	case All:
		off, n := op.Off(), op.Arg()

		for i := range n {
			nv, err := c.apply(c.s.prog.code[off+i], val, h)
			if err != nil {
				return nv, err
			}

			val = nv
		}
	case Type:
		c.checkType(op, val)
	case Properties:
		return c.checkProps(op, val, h)
	case Required:
		c.checkRequired(op, val)
	case MinProps:
		if val.Op() == Object && val.Arg() < op.Imm() {
			c.Fail(op, val, "too few properties")
		}
	case MaxProps:
		if val.Op() == Object && val.Arg() > op.Imm() {
			c.Fail(op, val, "too many properties")
		}
	case Items:
		return c.checkItems(op, val, h)
	case MinItems:
		if val.Op() == Array && val.Arg() < op.Imm() {
			c.Fail(op, val, "too few items")
		}
	case MaxItems:
		if val.Op() == Array && val.Arg() > op.Imm() {
			c.Fail(op, val, "too many items")
		}
	case Unique:
		c.checkUnique(op, val)
	case MinLen:
		if val.Op() == Str && c.strlen(val) < op.Imm() {
			c.Fail(op, val, "too short")
		}
	case MaxLen:
		if val.Op() == Str && c.strlen(val) > op.Imm() {
			c.Fail(op, val, "too long")
		}
	case Minimum:
		if val.Op() == Num && c.number(val) < c.schemaNum(op) {
			c.Fail(op, val, "less than minimum")
		}
	case Maximum:
		if val.Op() == Num && c.number(val) > c.schemaNum(op) {
			c.Fail(op, val, "greater than maximum")
		}
	case ExclMin:
		if val.Op() == Num && c.number(val) <= c.schemaNum(op) {
			c.Fail(op, val, "not above exclusive minimum")
		}
	case ExclMax:
		if val.Op() == Num && c.number(val) >= c.schemaNum(op) {
			c.Fail(op, val, "not below exclusive maximum")
		}
	case MultipleOf:
		if val.Op() == Num && !c.multipleOf(op, val) {
			c.Fail(op, val, "not a multiple")
		}
	case Enum:
		c.checkEnum(op, val)
	case Const:
		if !c.equalLit(val, c.s.prog.code[op.Off()]) {
			c.Fail(op, val, "not the const value")
		}
	case Not:
		ok, err := c.matches(c.s.prog.code[op.Off()], val, h)
		if err != nil {
			return val, err
		}

		if ok {
			c.Fail(op, val, "matches a forbidden schema")
		}
	case AllOf:
		off, n := op.Off(), op.Arg()

		for i := range n {
			if _, err := c.apply(c.s.prog.code[off+i], val, h); err != nil {
				return val, err
			}
		}
	case AnyOf:
		if err := c.checkAnyOf(op, val, h); err != nil {
			return val, err
		}
	case OneOf:
		if err := c.checkOneOf(op, val, h); err != nil {
			return val, err
		}
	case Ref:
		// An external ref lives in another document's program arena; swap it in for
		// the subtree (the data arena c.b stays put), then restore.
		ts, tnode, err := c.s.refResolve(op)
		if err != nil {
			return val, err
		}

		if ts == c.s {
			return c.apply(tnode, val, h)
		}

		saved := c.s
		c.s = ts
		v, err := c.apply(tnode, val, h)
		c.s = saved

		return v, err
	case Additional:
		return c.checkAdditional(op, val, h)
	case PatternProps:
		return c.checkPatternProps(op, val, h)
	case Pattern:
		if val.Op() == Str && !c.s.patterns[op].Match(c.b.Reader().String(val)) {
			c.Fail(op, val, "does not match pattern")
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

func (c *cur) applyChild(sub, val, step Opcode, h Handler) (Opcode, error) {
	mark := len(c.spath)

	c.spath = append(c.spath, sub)
	c.dpath = append(c.dpath, step)

	defer func() {
		c.spath = c.spath[:mark]
		c.dpath = c.dpath[:mark]
	}()

	return c.apply(sub, val, h)
}

func (c *cur) checkType(op, val Opcode) {
	mask := op.ImmInt()
	t := dataType(val)

	ok := mask&t != 0
	if t == typeNum && mask&typeInt != 0 && c.integral(val) {
		ok = true
	}

	if !ok {
		c.Fail(op, val, "wrong type")
	}
}

func (c *cur) checkProps(op, val Opcode, h Handler) (Opcode, error) {
	seek := c.seeking()

	switch seek.Op() {
	case None, Key, Each:
		// ok
	case IntLit:
		c.Fail(op, None, "schema is object, val supposes array")
		return val, nil
	default:
		panic(seek)
	}

	if val.Op() != Object {
		return val, nil
	}

	if !c.rewrite {
		return val, c.validateProps(op, val, h)
	}

	return c.rewriteProps(op, val, h)
}

func (c *cur) validateProps(op, val Opcode, h Handler) error {
	seek := c.seeking()
	off, n := op.Off(), op.Arg()

	for i := range n {
		name := c.s.prog.code[off+2*i]
		sub := c.s.prog.code[off+2*i+1]

		if seek.Op() == Key && !c.idEq(seek, name) {
			continue
		}

		key, v, ok := c.member(val, name)
		if !ok {
			continue
		}

		if _, err := c.applyChild(sub, v, key, h); err != nil {
			return err
		}
	}

	return nil
}

func (c *cur) rewriteProps(op, val Opcode, h Handler) (Opcode, error) {
	mark := len(c.b.tmp)
	defer func() { c.b.tmp = c.b.tmp[:mark] }()

	var dirty bool
	var err error

	if c.s.Flags.Is(KeepKeyOrder) {
		dirty, err = c.orderedProps(op, val, h)
	} else {
		dirty, err = c.canonProps(op, val, h)
	}

	if err != nil {
		return val, err
	}

	if !dirty {
		return val, nil
	}

	return c.b.Writer().Object(c.b.tmp[mark:]...), nil
}

func (c *cur) orderedProps(op, val Opcode, h Handler) (bool, error) {
	dirty := false

	voff, vn := val.Off(), val.Arg()

	for i := range vn {
		key := c.b.code[voff+2*i]
		v := c.b.code[voff+2*i+1]

		if sub, ok := c.propSub(op, key); ok {
			nv, err := c.applyChild(sub, v, key, h)
			if err != nil {
				return dirty, err
			}

			if nv != v {
				v = nv
				dirty = true
			}
		}

		c.b.tmp = append(c.b.tmp, key, v)
	}

	if c.s.Flags.Is(KeepMissing) {
		return dirty, nil
	}

	off, n := op.Off(), op.Arg()

	for i := range n {
		name := c.s.prog.code[off+2*i]

		if _, _, ok := c.member(val, name); ok {
			continue
		}

		if dv, ok := c.defaultOf(c.s.prog.code[off+2*i+1]); ok {
			c.b.tmp = append(c.b.tmp, c.copyLit(name), c.copyLit(dv))
			dirty = true
		}
	}

	return dirty, nil
}

func (c *cur) canonProps(op, val Opcode, h Handler) (bool, error) {
	voff, vn := val.Off(), val.Arg()

	dirty := false
	j := int64(0) // source member slot the next emitted pair is compared against

	off, n := op.Off(), op.Arg()

	for i := range n {
		name := c.s.prog.code[off+2*i]
		sub := c.s.prog.code[off+2*i+1]

		if key, v, ok := c.member(val, name); ok {
			nv, err := c.applyChild(sub, v, key, h)
			if err != nil {
				return dirty, err
			}

			v = nv

			if key != c.b.code[voff+2*j] || v != c.b.code[voff+2*j+1] {
				dirty = true
			}

			c.b.tmp = append(c.b.tmp, key, v)
			j++
			continue
		}

		if c.s.Flags.Is(KeepMissing) {
			continue
		}

		if dv, ok := c.defaultOf(sub); ok {
			c.b.tmp = append(c.b.tmp, c.copyLit(name), c.copyLit(dv))
			dirty = true
		}
	}

	for i := range vn {
		key := c.b.code[voff+2*i]
		v := c.b.code[voff+2*i+1]

		if _, ok := c.propSub(op, key); ok {
			continue
		}

		if key != c.b.code[voff+2*j] || v != c.b.code[voff+2*j+1] {
			dirty = true
		}

		c.b.tmp = append(c.b.tmp, key, v)
		j++
	}

	return dirty, nil
}

func (c *cur) propSub(op, key Opcode) (Opcode, bool) {
	off, n := op.Off(), op.Arg()

	for i := range n {
		if c.keyEq(key, c.s.prog.code[off+2*i]) {
			return c.s.prog.code[off+2*i+1], true
		}
	}

	return 0, false
}

func (c *cur) defaultOf(sub Opcode) (Opcode, bool) {
	if sub.Op() != All {
		return 0, false
	}

	for _, ch := range c.s.prog.Reader().Nodes(sub) {
		if ch.Op() == Default {
			return c.s.prog.code[ch.Off()], true
		}
	}

	return 0, false
}

// copyLit lifts a schema-arena literal (a property name or default value) into
// the data arena.
func (c *cur) copyLit(op Opcode) Opcode {
	return c.b.Writer().CopyFrom(c.s.prog.Reader(), op)
}

func (c *cur) checkAdditional(op, val Opcode, h Handler) (Opcode, error) {
	if val.Op() != Object {
		return val, nil
	}

	props, patterns, sub := c.s.additionalParts(op)

	if !c.rewrite {
		return val, c.validateAdditional(props, patterns, sub, val, h)
	}

	return c.rewriteAdditional(props, patterns, sub, val, h)
}

func (c *cur) validateAdditional(props, patterns, sub, val Opcode, h Handler) error {
	voff, vn := val.Off(), val.Arg()

	for i := range vn {
		key := c.b.code[voff+2*i]
		v := c.b.code[voff+2*i+1]

		if c.covered(props, patterns, key) {
			continue
		}

		if !c.memberSought(key) {
			continue
		}

		if _, err := c.applyChild(sub, v, key, h); err != nil {
			return err
		}
	}

	return nil
}

func (c *cur) rewriteAdditional(props, patterns, sub, val Opcode, h Handler) (Opcode, error) {
	mark := len(c.b.tmp)
	defer func() { c.b.tmp = c.b.tmp[:mark] }()

	voff, vn := val.Off(), val.Arg()
	dirty := false

	for i := range vn {
		key := c.b.code[voff+2*i]
		v := c.b.code[voff+2*i+1]

		if !c.covered(props, patterns, key) {
			nv, err := c.applyChild(sub, v, key, h)
			if err != nil {
				return val, err
			}

			if nv != v {
				v = nv
				dirty = true
			}
		}

		c.b.tmp = append(c.b.tmp, key, v)
	}

	if !dirty {
		return val, nil
	}

	return c.b.Writer().Object(c.b.tmp[mark:]...), nil
}

// covered reports whether key is named in the sibling properties node or matched
// by one of the sibling patternProperties — either way it is not additional.
func (c *cur) covered(props, patterns, key Opcode) bool {
	if props.Op() == Properties {
		if _, ok := c.propSub(props, key); ok {
			return true
		}
	}

	return c.patternHit(patterns, key)
}

// patternHit reports whether key matches any regex in a patternProperties node.
func (c *cur) patternHit(patterns, key Opcode) bool {
	if patterns.Op() != PatternProps {
		return false
	}

	off, n := patterns.Off(), patterns.Arg()

	for i := range n {
		if c.s.patterns[c.s.prog.code[off+2*i]].Match(c.b.Reader().String(key)) {
			return true
		}
	}

	return false
}

func (c *cur) checkPatternProps(op, val Opcode, h Handler) (Opcode, error) {
	if val.Op() != Object {
		return val, nil
	}

	mark := len(c.b.tmp)
	defer func() { c.b.tmp = c.b.tmp[:mark] }()

	off, n := op.Off(), op.Arg()
	voff, vn := val.Off(), val.Arg()
	dirty := false

	for i := range vn {
		key := c.b.code[voff+2*i]
		v := c.b.code[voff+2*i+1]

		if !c.memberSought(key) {
			c.b.tmp = append(c.b.tmp, key, v)
			continue
		}

		for j := range n {
			pat := c.s.prog.code[off+2*j]
			sub := c.s.prog.code[off+2*j+1]

			if !c.s.patterns[pat].Match(c.b.Reader().String(key)) {
				continue
			}

			nv, err := c.applyChild(sub, v, key, h)
			if err != nil {
				return val, err
			}

			if nv != v {
				v = nv
				dirty = true
			}
		}

		c.b.tmp = append(c.b.tmp, key, v)
	}

	if !c.rewrite || !dirty {
		return val, nil
	}

	return c.b.Writer().Object(c.b.tmp[mark:]...), nil
}

func (c *cur) checkRequired(op, val Opcode) {
	if c.seeking() != None {
		return
	}

	if val.Op() != Object {
		return
	}

	off, n := op.Off(), op.Arg()

	for i := range n {
		if _, _, ok := c.member(val, c.s.prog.code[off+i]); !ok {
			c.Fail(op, val, "missing required property")
		}
	}
}

func (c *cur) checkItems(op, val Opcode, h Handler) (Opcode, error) {
	if val.Op() != Array {
		return val, nil
	}

	seek := c.seeking()

	switch seek.Op() {
	case None, Each, IntLit:
		// ok
	case Key:
		c.Fail(op, None, "schema is array, val supposes object")
		return val, nil
	default:
		panic(seek)
	}

	mark := len(c.b.tmp)
	defer func() { c.b.tmp = c.b.tmp[:mark] }()

	sub := c.s.prog.code[op.Off()]
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

		v := c.b.code[voff+i]

		nv, err := c.applyChild(sub, v, makeImm(IntLit, int(i)), h)
		if err != nil {
			return val, err
		}

		if nv != v {
			dirty = true
		}

		c.b.tmp = append(c.b.tmp, nv)
	}

	if !dirty {
		return val, nil
	}

	return c.b.Writer().Array(c.b.tmp[mark:]...), nil
}

func (c *cur) checkUnique(op, val Opcode) {
	if val.Op() != Array {
		return
	}

	voff, vn := val.Off(), val.Arg()

	for i := range vn {
		for j := i + 1; j < vn; j++ {
			if equalBuf(c.b.Reader(), c.b.code[voff+i], c.b.Reader(), c.b.code[voff+j]) {
				c.Fail(op, val, "duplicate items")
				return
			}
		}
	}
}

func (c *cur) checkEnum(op, val Opcode) {
	off, n := op.Off(), op.Arg()

	for i := range n {
		if c.equalLit(val, c.s.prog.code[off+i]) {
			return
		}
	}

	c.Fail(op, val, "not in enum")
}

func (c *cur) checkAnyOf(op, val Opcode, h Handler) error {
	off, n := op.Off(), op.Arg()

	for i := range n {
		ok, err := c.matches(c.s.prog.code[off+i], val, h)
		if err != nil {
			return err
		}

		if ok {
			return nil
		}
	}

	c.Fail(op, val, "matches none of the schemas")

	return nil
}

func (c *cur) checkOneOf(op, val Opcode, h Handler) error {
	off, n := op.Off(), op.Arg()
	cnt := 0

	for i := range n {
		ok, err := c.matches(c.s.prog.code[off+i], val, h)
		if err != nil {
			return err
		}

		if ok {
			cnt++
		}
	}

	if cnt != 1 {
		c.Fail(op, val, "must match exactly one schema")
	}

	return nil
}

// matches calls apply, but drops diag messages.
func (c *cur) matches(op, val Opcode, h Handler) (bool, error) {
	n := len(c.diag)
	defer func() { c.diag = c.diag[:n] }()

	if _, err := c.apply(op, val, h); err != nil {
		return false, err
	}

	return len(c.diag) == n, nil
}

func (c *cur) member(obj, key Opcode) (k, v Opcode, ok bool) {
	voff, vn := obj.Off(), obj.Arg()

	for i := range vn {
		if c.keyEq(c.b.code[voff+2*i], key) {
			return c.b.code[voff+2*i], c.b.code[voff+2*i+1], true
		}
	}

	return 0, 0, false
}

func (c *cur) keyEq(data, schema Opcode) bool {
	return bytes.Equal(c.b.Reader().Span(data), c.s.prog.Reader().Span(schema))
}

func (c *cur) idEq(id, schema Opcode) bool {
	return bytes.Equal(c.b.Reader().Span(id), c.s.prog.Reader().String(schema))
}

func (c *cur) memberSought(key Opcode) bool {
	seek := c.seeking()

	switch seek.Op() {
	case None, Each:
		return true
	case Key:
		return bytes.Equal(c.b.Reader().Span(seek), c.b.Reader().String(key))
	default:
		return false
	}
}

func (c *cur) equalLit(val, lit Opcode) bool {
	return equalBuf(c.b.Reader(), val, c.s.prog.Reader(), lit)
}

func (c *cur) number(val Opcode) float64 {
	v, _ := json2.Value(c.b.Reader().Span(val)).Float64()
	return v
}

func (c *cur) schemaNum(op Opcode) float64 {
	lit := c.s.prog.code[op.Off()]
	v, _ := json2.Value(c.s.prog.Reader().Span(lit)).Float64()
	return v
}

func (c *cur) multipleOf(op, val Opcode) bool {
	lit := c.s.prog.code[op.Off()]

	ok, exact := isMultiple(c.b.Reader().Span(val), c.s.prog.Reader().Span(lit))
	if exact {
		return ok
	}

	m := c.schemaNum(op)
	return m == 0 || math.Mod(c.number(val), m) == 0
}

func (c *cur) integral(val Opcode) bool {
	v := c.number(val)
	return v == math.Trunc(v)
}

func (c *cur) strlen(val Opcode) int64 {
	var d json2.Iterator

	_, rs, _, _ := d.DecodedStringLength(c.b.src, val.OffInt())
	return int64(rs)
}

func (c *cur) Fail(op, val Opcode, msg string) {
	off, end := c.b.Reader().span(val)
	c.diag = append(c.diag, Diag{Off: off, End: end, Op: op.Op(), Message: msg})
}

// result reports only real failures — a broken document, a handler error, a
// failed ref. Validation diagnostics are not errors: the engine did its job, the
// findings are in c.diag for the caller to read.
func (c *cur) result(err error) error {
	if errors.Is(err, ErrBreak) {
		return nil
	}

	return err
}

func (c *cur) seeking() Opcode {
	if len(c.dpath) < len(c.at) {
		return c.at[len(c.dpath)]
	}

	return None
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
