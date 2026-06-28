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

		b Buffer // decoded data

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
	c.rewrite = true

	root, err := c.b.decode(r)
	if err != nil {
		return w, nil, err
	}

	out := c.apply(s.root, root)
	w = c.b.encode(w, out)

	return w, c.diag, diagsError(c.diag)
}

// apply runs program node op against data value val and returns the rewritten
// value (val unchanged for now). Validation issues are collected in c.diag.
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
		c.checkProps(op, val)
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

func (c *cur) checkProps(op, val Opcode) {
	if val.Op() != Object {
		return
	}

	off, n := op.off(), op.arg()

	for i := range n {
		key := c.s.code[off+2*i]
		sub := c.s.code[off+2*i+1]

		mv, ok := c.member(val, key)
		if !ok {
			continue // absent: default handling is a later slice
		}

		c.apply(sub, mv)
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
			if equalBuf(&c.b, c.b.code[off+i], &c.b, c.b.code[off+j]) {
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
	return bytes.Equal(data.str(c.b.src), schema.str(c.s.schema))
}

func (c *cur) equalLit(val, lit Opcode) bool {
	sb := Buffer{code: c.s.code, src: c.s.schema}
	return equalBuf(&c.b, val, &sb, lit)
}

func (c *cur) number(val Opcode) float64 {
	v, _ := json2.Value(val.str(c.b.src)).Float64()
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
		lv, _ := json2.Value(l.str(lb.src)).Float64()
		rv, _ := json2.Value(r.str(rb.src)).Float64()

		return lv == rv
	case Str:
		return bytes.Equal(l.str(lb.src), r.str(rb.src))
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
