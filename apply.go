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

		rewrite bool
		diag    []Diag
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

var ErrInvalid = errors.New("invalid")

func (s *Schema) Validate(r []byte) ([]Diag, error) {
	var c cur
	c.s = s
	c.b = &s.b

	root, err := c.b.decode(r)
	if err != nil {
		return nil, err
	}

	c.apply(s.root, root)

	return c.diag, diagsError(c.diag)
}

func (s *Schema) Rewrite(w, r []byte) ([]byte, []Diag, error) {
	var c cur
	c.s = s
	c.b = &s.b
	c.rewrite = true

	root, err := c.b.decode(r)
	if err != nil {
		return w, nil, err
	}

	out := c.apply(s.root, root)
	w = c.b.encode(w, out)

	return w, c.diag, diagsError(c.diag)
}

// Two arenas, walked in parallel but never interlinked — each node's spans
// point only into its own bytes:
//
//	schema  nodes s.code   | bytes s.schema            (read-only literals)
//	data    nodes c.b.code | bytes c.b.src ++ c.b.text
//
// A block payload (off,count) indexes its arena's nodes; a span (off,len) its
// bytes. Bytes slices are read-only, so rewrites that synthesize literals
// (defaults, canon) copy the bytes into the writable c.b.text tail and build
// nodes in c.b.code (data is where changes live). Data spans resolve a virtual
// src++text concat by off vs len(src), so the input is never copied.

// apply runs program node op against data value val and returns the (possibly
// rewritten) value. Validation issues are collected in c.diag.
func (c *cur) apply(op, val Opcode) Opcode {
	// Type-specific keywords are guarded on the value's type: per spec a keyword
	// that doesn't apply to the instance type imposes no constraint (it passes).
	// Only Type constrains the type itself.
	switch op.Op() {
	case Pass:
	case Fail:
		c.fail(op, val, "schema forbids any value")
	case And:
		off, n := op.off(), op.arg()

		for i := range n {
			val = c.apply(c.s.code[off+i], val)
		}
	case Type:
		c.checkType(op, val)
	case Properties:
		val = c.checkProps(op, val)
	case Required:
		c.checkRequired(op, val)
	case MinProps:
		if val.Op() == Object && val.arg() < op.imm() {
			c.fail(op, val, "too few properties")
		}
	case MaxProps:
		if val.Op() == Object && val.arg() > op.imm() {
			c.fail(op, val, "too many properties")
		}
	case Items:
		c.checkItems(op, val)
	case MinItems:
		if val.Op() == Array && val.arg() < op.imm() {
			c.fail(op, val, "too few items")
		}
	case MaxItems:
		if val.Op() == Array && val.arg() > op.imm() {
			c.fail(op, val, "too many items")
		}
	case Unique:
		c.checkUnique(op, val)
	case MinLen:
		if val.Op() == Str && c.strlen(val) < op.imm() {
			c.fail(op, val, "too short")
		}
	case MaxLen:
		if val.Op() == Str && c.strlen(val) > op.imm() {
			c.fail(op, val, "too long")
		}
	case Minimum:
		if val.Op() == Num && c.number(val) < c.schemaNum(op) {
			c.fail(op, val, "less than minimum")
		}
	case Maximum:
		if val.Op() == Num && c.number(val) > c.schemaNum(op) {
			c.fail(op, val, "greater than maximum")
		}
	case ExclMin:
		if val.Op() == Num && c.number(val) <= c.schemaNum(op) {
			c.fail(op, val, "not above exclusive minimum")
		}
	case ExclMax:
		if val.Op() == Num && c.number(val) >= c.schemaNum(op) {
			c.fail(op, val, "not below exclusive maximum")
		}
	case MultipleOf:
		m := c.schemaNum(op)
		if val.Op() == Num && m != 0 && math.Mod(c.number(val), m) != 0 {
			c.fail(op, val, "not a multiple")
		}
	case Enum:
		c.checkEnum(op, val)
	case Const:
		if !c.equalLit(val, c.s.code[op.off()]) {
			c.fail(op, val, "not the const value")
		}
	case Not:
		if c.matches(c.s.code[op.off()], val) {
			c.fail(op, val, "matches a forbidden schema")
		}
	case AllOf:
		off, n := op.off(), op.arg()

		for i := range n {
			c.apply(c.s.code[off+i], val)
		}
	case AnyOf:
		c.checkAnyOf(op, val)
	case OneOf:
		c.checkOneOf(op, val)
	case Ref:
		val = c.apply(c.s.refTarget(op), val)
	case Raw:
		// annotation kept for round-trip, no constraint
	case Additional, Pattern, Default:
		// TODO: additionalProperties (cross-keyword), pattern (regex), default (rewrite)
	default:
		panic(op)
	}

	return val
}

func (c *cur) checkType(op, val Opcode) {
	mask := op.imm()
	t := dataType(val)

	ok := mask&t != 0
	if t == typeNum && mask&typeInt != 0 && c.integral(val) {
		ok = true
	}

	if !ok {
		c.fail(op, val, "wrong type")
	}
}

func (c *cur) checkProps(op, val Opcode) Opcode {
	if val.Op() != Object {
		return val
	}

	if !c.rewrite {
		c.validateProps(op, val)
		return val
	}

	return c.rewriteProps(op, val)
}

func (c *cur) validateProps(op, val Opcode) {
	off, n := op.off(), op.arg()

	for i := range n {
		key := c.s.code[off+2*i]
		sub := c.s.code[off+2*i+1]

		if mv, ok := c.member(val, key); ok {
			c.apply(sub, mv)
		}
	}
}

// rewriteProps rebuilds the object with rewritten members and inserted defaults,
// returning val unchanged when nothing moved (structural sharing).
func (c *cur) rewriteProps(op, val Opcode) Opcode {
	mark := len(c.b.tmp)
	defer func() { c.b.tmp = c.b.tmp[:mark] }()

	dirty := false

	voff, vn := val.off(), val.arg()

	for i := range vn {
		key := c.b.code[voff+2*i]
		v := c.b.code[voff+2*i+1]

		if sub, ok := c.propSub(op, key); ok {
			if nv := c.apply(sub, v); nv != v {
				v = nv
				dirty = true
			}
		}

		c.b.tmp = append(c.b.tmp, key, v)
	}

	off, n := op.off(), op.arg()

	for i := range n {
		key := c.s.code[off+2*i]

		if _, ok := c.member(val, key); ok {
			continue
		}

		if dv, ok := c.defaultOf(c.s.code[off+2*i+1]); ok {
			c.b.tmp = append(c.b.tmp, c.copyLit(key), c.copyLit(dv))
			dirty = true
		}
	}

	if !dirty {
		return val
	}

	cnt := (len(c.b.tmp) - mark) / 2
	noff := len(c.b.code)
	c.b.code = append(c.b.code, c.b.tmp[mark:]...)

	return makeNode(Object, noff, cnt)
}

func (c *cur) propSub(op, key Opcode) (Opcode, bool) {
	off, n := op.off(), op.arg()

	for i := range n {
		if c.keyEq(key, c.s.code[off+2*i]) {
			return c.s.code[off+2*i+1], true
		}
	}

	return 0, false
}

func (c *cur) defaultOf(sub Opcode) (Opcode, bool) {
	if sub.Op() != And {
		return 0, false
	}

	for _, ch := range sub.nodes(c.s.code) {
		if ch.Op() == Default {
			return c.s.code[ch.off()], true
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
		b := lit.str(c.s.schema)
		off := len(c.b.src) + len(c.b.text)
		c.b.text = append(c.b.text, b...)

		return makeNode(lit.Op(), off, len(b))
	case Array:
		mark := len(c.b.tmp)
		defer func() { c.b.tmp = c.b.tmp[:mark] }()

		for _, ch := range lit.nodes(c.s.code) {
			c.b.tmp = append(c.b.tmp, c.copyLit(ch))
		}

		cnt := len(c.b.tmp) - mark
		off := len(c.b.code)
		c.b.code = append(c.b.code, c.b.tmp[mark:]...)

		return makeNode(Array, off, cnt)
	case Object:
		mark := len(c.b.tmp)
		defer func() { c.b.tmp = c.b.tmp[:mark] }()

		o, n := lit.off(), lit.arg()

		for i := range n {
			c.b.tmp = append(c.b.tmp, c.copyLit(c.s.code[o+2*i]), c.copyLit(c.s.code[o+2*i+1]))
		}

		cnt := (len(c.b.tmp) - mark) / 2
		off := len(c.b.code)
		c.b.code = append(c.b.code, c.b.tmp[mark:]...)

		return makeNode(Object, off, cnt)
	default:
		panic(lit)
	}
}

func (c *cur) checkRequired(op, val Opcode) {
	if val.Op() != Object {
		return
	}

	off, n := op.off(), op.arg()

	for i := range n {
		if _, ok := c.member(val, c.s.code[off+i]); !ok {
			c.fail(op, val, "missing required property")
		}
	}
}

func (c *cur) checkItems(op, val Opcode) {
	if val.Op() != Array {
		return
	}

	sub := c.s.code[op.off()]
	off, n := val.off(), val.arg()

	for i := range n {
		c.apply(sub, c.b.code[off+i])
	}
}

func (c *cur) checkUnique(op, val Opcode) {
	if val.Op() != Array {
		return
	}

	off, n := val.off(), val.arg()

	for i := range n {
		for j := i + 1; j < n; j++ {
			if equalBuf(c.b, c.b.code[off+i], c.b, c.b.code[off+j]) {
				c.fail(op, val, "duplicate items")
				return
			}
		}
	}
}

func (c *cur) checkEnum(op, val Opcode) {
	off, n := op.off(), op.arg()

	for i := range n {
		if c.equalLit(val, c.s.code[off+i]) {
			return
		}
	}

	c.fail(op, val, "not in enum")
}

func (c *cur) checkAnyOf(op, val Opcode) {
	off, n := op.off(), op.arg()

	for i := range n {
		if c.matches(c.s.code[off+i], val) {
			return
		}
	}

	c.fail(op, val, "matches none of the schemas")
}

func (c *cur) checkOneOf(op, val Opcode) {
	off, n := op.off(), op.arg()
	cnt := 0

	for i := range n {
		if c.matches(c.s.code[off+i], val) {
			cnt++
		}
	}

	if cnt != 1 {
		c.fail(op, val, "must match exactly one schema")
	}
}

// matches reports whether val satisfies op, discarding any diagnostics the
// trial produced.
func (c *cur) matches(op, val Opcode) bool {
	n := len(c.diag)

	c.apply(op, val)

	ok := len(c.diag) == n
	c.diag = c.diag[:n]

	return ok
}

func (c *cur) member(obj, key Opcode) (Opcode, bool) {
	off, n := obj.off(), obj.arg()

	for i := range n {
		if c.keyEq(c.b.code[off+2*i], key) {
			return c.b.code[off+2*i+1], true
		}
	}

	return 0, false
}

func (c *cur) keyEq(data, schema Opcode) bool {
	return bytes.Equal(c.b.span(data), schema.str(c.s.schema))
}

func (c *cur) equalLit(val, lit Opcode) bool {
	sb := Buffer{code: c.s.code, src: c.s.schema}
	return equalBuf(c.b, val, &sb, lit)
}

func (c *cur) number(val Opcode) float64 {
	v, _ := json2.Value(c.b.span(val)).Float64()
	return v
}

func (c *cur) schemaNum(op Opcode) float64 {
	lit := c.s.code[op.off()]
	v, _ := json2.Value(lit.str(c.s.schema)).Float64()
	return v
}

func (c *cur) integral(val Opcode) bool {
	v := c.number(val)
	return v == math.Trunc(v)
}

func (c *cur) strlen(val Opcode) int {
	var d json2.Iterator

	_, rs, _, _ := d.DecodedStringLength(c.b.src, val.off())
	return rs
}

func (c *cur) fail(op, val Opcode, msg string) {
	d := Diag{Op: op.Op(), Level: Error, Msg: msg}

	if sh := val.Op(); sh == Num || sh == Str {
		d.Off, d.Len = val.off(), val.arg()
	}

	c.diag = append(c.diag, d)
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
		lv, _ := json2.Value(lb.span(l)).Float64()
		rv, _ := json2.Value(rb.span(r)).Float64()

		return lv == rv
	case Str:
		return bytes.Equal(lb.span(l), rb.span(r))
	case Array:
		lo, ln := l.off(), l.arg()
		ro, rn := r.off(), r.arg()

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
		lo, ln := l.off(), l.arg()
		ro, rn := r.off(), r.arg()

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
