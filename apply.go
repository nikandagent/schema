package schema

import (
	"bytes"
	"errors"
	"math"

	"nikand.dev/go/json2"
)

type (
	cur struct {
		s *Schema // program
		b *Buffer // decoded data (reused &s.b)

		h Handler // Walk override, nil for Validate/Rewrite

		rewrite bool
		diag    []Diag
	}

	// Handler runs in place of the default apply for a node during Walk and
	// WalkRewrite. Return the (possibly rewritten) value; call c.Apply to
	// delegate to the default. Walk ignores the returned value. Return ErrBreak
	// to stop the walk cleanly.
	Handler = func(c Applier, op, val Opcode) (Opcode, error)

	// Applier is the engine a Handler talks to. cur implements it; it is an
	// interface so cur stays free to change without breaking handlers.
	Applier interface {
		Apply(op, val Opcode) (Opcode, error)
		Fail(op, val Opcode, msg string)
		Buf() *Buffer       // data arena (the value side)
		SchemaBuf() *Buffer // program arena (the op side)
	}

	Diag struct {
		Off, Len int    // offending span in the input (0,0 for containers, TODO)
		Op       Opcode // failed keyword
		Level    Level
		Msg      string
	}

	Level int
)

const (
	Error Level = iota
	Warning
	Info
)

var (
	ErrInvalid = errors.New("invalid")
	ErrBreak   = errors.New("break") // a Handler returns it to stop the walk cleanly
)

func (s *Schema) Validate(r []byte) ([]Diag, error) {
	return s.Walk(r, nil)
}

func (s *Schema) Rewrite(w, r []byte) ([]byte, []Diag, error) {
	return s.WalkRewrite(w, r, nil)
}

// Walk traverses the schema against the data without mutating it, running h at
// each node (nil h is the default validator). h's returned value is ignored.
func (s *Schema) Walk(r []byte, h Handler) ([]Diag, error) {
	c := cur{
		s: s,
		b: &s.b,
		h: h,
	}

	root, err := c.b.decode(r)
	if err != nil {
		return nil, err
	}

	_, err = c.apply(s.root, root)

	return c.diag, c.result(err)
}

// WalkRewrite is Walk that encodes the (possibly rewritten) value to w. nil h is
// the default rewriter.
func (s *Schema) WalkRewrite(w, r []byte, h Handler) ([]byte, []Diag, error) {
	c := cur{
		s:       s,
		b:       &s.b,
		h:       h,
		rewrite: true,
	}

	root, err := c.b.decode(r)
	if err != nil {
		return w, nil, err
	}

	out, err := c.apply(s.root, root)

	if e := c.result(err); e != nil {
		return w, c.diag, e
	}

	return c.b.encode(w, out), c.diag, nil
}

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

// apply dispatches one node: a Walk handler sees it first (and may rewrite it or
// recurse via Apply); otherwise the default behaviour runs.
func (c *cur) apply(op, val Opcode) (Opcode, error) {
	if c.h == nil {
		return c.applyDefault(op, val)
	}

	return c.h(c, op, val)
}

// Apply runs the default behaviour for a node — the handler's delegate point.
// Its recursions dispatch back through the handler.
func (c *cur) Apply(op, val Opcode) (Opcode, error) {
	return c.applyDefault(op, val)
}

func (c *cur) Buf() *Buffer       { return c.b }
func (c *cur) SchemaBuf() *Buffer { return &c.s.prog }

// applyDefault runs program node op against data value val and returns the
// (possibly rewritten) value. Validation issues are collected in c.diag; a
// non-nil error is a hard stop raised by a handler.
func (c *cur) applyDefault(op, val Opcode) (Opcode, error) {
	// Type-specific keywords are guarded on the value's type: per spec a keyword
	// that doesn't apply to the instance type imposes no constraint (it passes).
	// Only Type constrains the type itself.
	switch op.Op() {
	case Pass:
	case Fail:
		c.Fail(op, val, "schema forbids any value")
	case And:
		off, n := op.Off(), op.Arg()

		for i := range n {
			nv, err := c.apply(c.s.prog.code[off+i], val)
			if err != nil {
				return nv, err
			}

			val = nv
		}
	case Type:
		c.checkType(op, val)
	case Properties:
		return c.checkProps(op, val)
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
		if err := c.checkItems(op, val); err != nil {
			return val, err
		}
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
		m := c.schemaNum(op)
		if val.Op() == Num && m != 0 && math.Mod(c.number(val), m) != 0 {
			c.Fail(op, val, "not a multiple")
		}
	case Enum:
		c.checkEnum(op, val)
	case Const:
		if !c.equalLit(val, c.s.prog.code[op.Off()]) {
			c.Fail(op, val, "not the const value")
		}
	case Not:
		ok, err := c.matches(c.s.prog.code[op.Off()], val)
		if err != nil {
			return val, err
		}

		if ok {
			c.Fail(op, val, "matches a forbidden schema")
		}
	case AllOf:
		off, n := op.Off(), op.Arg()

		for i := range n {
			if _, err := c.apply(c.s.prog.code[off+i], val); err != nil {
				return val, err
			}
		}
	case AnyOf:
		if err := c.checkAnyOf(op, val); err != nil {
			return val, err
		}
	case OneOf:
		if err := c.checkOneOf(op, val); err != nil {
			return val, err
		}
	case Ref:
		return c.apply(c.s.refTarget(op), val)
	case Raw:
		// annotation kept for round-trip, no constraint
	case Additional, Pattern, Default:
		// TODO: additionalProperties (cross-keyword), pattern (regex), default (rewrite)
	default:
		panic(op)
	}

	return val, nil
}

func (c *cur) checkType(op, val Opcode) {
	mask := op.Imm()
	t := dataType(val)

	ok := mask&t != 0
	if t == typeNum && mask&typeInt != 0 && c.integral(val) {
		ok = true
	}

	if !ok {
		c.Fail(op, val, "wrong type")
	}
}

func (c *cur) checkProps(op, val Opcode) (Opcode, error) {
	if val.Op() != Object {
		return val, nil
	}

	if !c.rewrite {
		return val, c.validateProps(op, val)
	}

	return c.rewriteProps(op, val)
}

func (c *cur) validateProps(op, val Opcode) error {
	off, n := op.Off(), op.Arg()

	for i := range n {
		key := c.s.prog.code[off+2*i]
		sub := c.s.prog.code[off+2*i+1]

		if _, mv, ok := c.member(val, key); ok {
			if _, err := c.apply(sub, mv); err != nil {
				return err
			}
		}
	}

	return nil
}

// rewriteProps rebuilds the object with rewritten members and inserted defaults,
// returning val unchanged when nothing moved (structural sharing).
func (c *cur) rewriteProps(op, val Opcode) (Opcode, error) {
	mark := len(c.b.tmp)
	defer func() { c.b.tmp = c.b.tmp[:mark] }()

	var dirty bool
	var err error

	if c.s.Flags.Is(KeepKeyOrder) {
		dirty, err = c.orderedProps(op, val)
	} else {
		dirty, err = c.canonProps(op, val)
	}

	if err != nil {
		return val, err
	}

	if !dirty {
		return val, nil
	}

	out := c.b.tmp[mark:]
	n := len(out) / 2
	off := len(c.b.code)
	c.b.code = append(c.b.code, out...)

	return makeNode(Object, off, n), nil
}

// orderedProps keeps the input key order, rewriting governed members in place
// and appending defaults for missing keys at the end. It reports whether
// anything changed.
func (c *cur) orderedProps(op, val Opcode) (bool, error) {
	dirty := false

	voff, vn := val.Off(), val.Arg()

	for i := range vn {
		key := c.b.code[voff+2*i]
		v := c.b.code[voff+2*i+1]

		if sub, ok := c.propSub(op, key); ok {
			nv, err := c.apply(sub, v)
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
		key := c.s.prog.code[off+2*i]

		if _, _, ok := c.member(val, key); ok {
			continue
		}

		if dv, ok := c.defaultOf(c.s.prog.code[off+2*i+1]); ok {
			c.b.tmp = append(c.b.tmp, c.copyLit(key), c.copyLit(dv))
			dirty = true
		}
	}

	return dirty, nil
}

// canonProps emits governed keys in declared properties order, inserting
// defaults into their natural slots, then the remaining keys in input order. It
// reports dirty when an emitted pair lands in a different slot than the source
// (reorder or rewritten value) or a default was inserted.
func (c *cur) canonProps(op, val Opcode) (bool, error) {
	voff, vn := val.Off(), val.Arg()

	dirty := false
	j := 0 // source member slot the next emitted pair is compared against

	off, n := op.Off(), op.Arg()

	for i := range n {
		key := c.s.prog.code[off+2*i]
		sub := c.s.prog.code[off+2*i+1]

		if dk, v, ok := c.member(val, key); ok {
			nv, err := c.apply(sub, v)
			if err != nil {
				return dirty, err
			}

			v = nv

			if dk != c.b.code[voff+2*j] || v != c.b.code[voff+2*j+1] {
				dirty = true
			}

			c.b.tmp = append(c.b.tmp, dk, v)
			j++
			continue
		}

		if c.s.Flags.Is(KeepMissing) {
			continue
		}

		if dv, ok := c.defaultOf(sub); ok {
			c.b.tmp = append(c.b.tmp, c.copyLit(key), c.copyLit(dv))
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
	if sub.Op() != And {
		return 0, false
	}

	for _, ch := range c.s.prog.Nodes(sub) {
		if ch.Op() == Default {
			return c.s.prog.code[ch.Off()], true
		}
	}

	return 0, false
}

// copyLit copies a schema literal into the data arena: scalar bytes into the
// writable text tail, container structure into the node arena.
func (c *cur) copyLit(lit Opcode) Opcode {
	switch lit.Op() {
	case Null, True, False:
		return lit
	case Num, Str:
		b := c.s.prog.Span(lit)
		off := len(c.b.src) + len(c.b.text)
		c.b.text = append(c.b.text, b...)

		return makeNode(lit.Op(), off, len(b))
	case Array:
		mark := len(c.b.tmp)
		defer func() { c.b.tmp = c.b.tmp[:mark] }()

		for _, ch := range c.s.prog.Nodes(lit) {
			c.b.tmp = append(c.b.tmp, c.copyLit(ch))
		}

		n := len(c.b.tmp) - mark
		off := len(c.b.code)
		c.b.code = append(c.b.code, c.b.tmp[mark:]...)

		return makeNode(Array, off, n)
	case Object:
		mark := len(c.b.tmp)
		defer func() { c.b.tmp = c.b.tmp[:mark] }()

		voff, vn := lit.Off(), lit.Arg()

		for i := range vn {
			c.b.tmp = append(c.b.tmp, c.copyLit(c.s.prog.code[voff+2*i]), c.copyLit(c.s.prog.code[voff+2*i+1]))
		}

		n := (len(c.b.tmp) - mark) / 2
		off := len(c.b.code)
		c.b.code = append(c.b.code, c.b.tmp[mark:]...)

		return makeNode(Object, off, n)
	default:
		panic(lit)
	}
}

func (c *cur) checkRequired(op, val Opcode) {
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

func (c *cur) checkItems(op, val Opcode) error {
	if val.Op() != Array {
		return nil
	}

	sub := c.s.prog.code[op.Off()]
	voff, vn := val.Off(), val.Arg()

	for i := range vn {
		if _, err := c.apply(sub, c.b.code[voff+i]); err != nil {
			return err
		}
	}

	return nil
}

func (c *cur) checkUnique(op, val Opcode) {
	if val.Op() != Array {
		return
	}

	voff, vn := val.Off(), val.Arg()

	for i := range vn {
		for j := i + 1; j < vn; j++ {
			if equalBuf(c.b, c.b.code[voff+i], c.b, c.b.code[voff+j]) {
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

func (c *cur) checkAnyOf(op, val Opcode) error {
	off, n := op.Off(), op.Arg()

	for i := range n {
		ok, err := c.matches(c.s.prog.code[off+i], val)
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

func (c *cur) checkOneOf(op, val Opcode) error {
	off, n := op.Off(), op.Arg()
	cnt := 0

	for i := range n {
		ok, err := c.matches(c.s.prog.code[off+i], val)
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

// matches reports whether val satisfies op, discarding any diagnostics the
// trial produced.
func (c *cur) matches(op, val Opcode) (bool, error) {
	n := len(c.diag)
	defer func() { c.diag = c.diag[:n] }()

	if _, err := c.apply(op, val); err != nil {
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
	return bytes.Equal(c.b.Span(data), c.s.prog.Span(schema))
}

func (c *cur) equalLit(val, lit Opcode) bool {
	return equalBuf(c.b, val, &c.s.prog, lit)
}

func (c *cur) number(val Opcode) float64 {
	v, _ := json2.Value(c.b.Span(val)).Float64()
	return v
}

func (c *cur) schemaNum(op Opcode) float64 {
	lit := c.s.prog.code[op.Off()]
	v, _ := json2.Value(c.s.prog.Span(lit)).Float64()
	return v
}

func (c *cur) integral(val Opcode) bool {
	v := c.number(val)
	return v == math.Trunc(v)
}

func (c *cur) strlen(val Opcode) int {
	var d json2.Iterator

	_, rs, _, _ := d.DecodedStringLength(c.b.src, val.Off())
	return rs
}

func (c *cur) Fail(op, val Opcode, msg string) {
	d := Diag{Op: op.Op(), Level: Error, Msg: msg}

	if sh := val.Op(); sh == Num || sh == Str {
		d.Off, d.Len = val.Off(), val.Arg()
	}

	c.diag = append(c.diag, d)
}

// result reports a hard error raised by a handler; ErrBreak is a clean stop, not
// an error, so it falls through to the diagnostics verdict.
func (c *cur) result(err error) error {
	if err != nil && err != ErrBreak {
		return err
	}

	return diagsError(c.diag)
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

// equalBuf reports JSON-semantic equality of value l in lb and value r in rb.
// Objects are compared in document order (pragmatic).
func equalBuf(lb *Buffer, l Opcode, rb *Buffer, r Opcode) bool {
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

		for i := range 2 * ln {
			if !equalBuf(lb, lb.code[lo+i], rb, rb.code[ro+i]) {
				return false
			}
		}

		return true
	default:
		return false
	}
}

func diagsError(d []Diag) error {
	for _, x := range d {
		if x.Level == Error {
			return ErrInvalid
		}
	}

	return nil
}
