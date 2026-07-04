package schema

import (
	"math/bits"
	"strconv"
)

// isMultiple reports whether the JSON number value is an exact integer multiple
// of divisor. exact is false when the inputs fall outside the bounded decimal
// (mantissa over 64 bits, or a scale-up past 128 bits) and the caller should use
// a floating-point fallback.
func isMultiple(value, divisor []byte) (ok, exact bool) {
	mv, sv, ok1 := parseDecimal(value)
	md, sd, ok2 := parseDecimal(divisor)
	if !ok1 || !ok2 || md == 0 {
		return false, false
	}

	// bring both to the common scale min(sv,sd); one side scales up by 10^diff.
	if sv >= sd {
		hi, lo, ok := scale128(mv, sv-sd)
		if !ok {
			return false, false
		}

		return mod128(hi, lo, md) == 0, true
	}

	hi, lo, ok := scale128(md, sd-sv)
	if !ok {
		return false, false
	}

	if hi != 0 { // divisor exceeds 2^64 > value, so value is a multiple only if zero
		return mv == 0, true
	}

	return mv%lo == 0, true
}

// parseDecimal reads a JSON number into mantissa*10^scale, ignoring sign.
func parseDecimal(b []byte) (mant uint64, scale int, ok bool) {
	i := 0
	if i < len(b) && (b[i] == '-' || b[i] == '+') {
		i++
	}

	seen, dot, frac := false, false, 0

	for ; i < len(b); i++ {
		c := b[i]

		switch {
		case c >= '0' && c <= '9':
			hi, lo := bits.Mul64(mant, 10)
			if hi != 0 {
				return 0, 0, false
			}

			lo += uint64(c - '0')
			if lo < uint64(c-'0') {
				return 0, 0, false
			}

			mant = lo
			seen = true

			if dot {
				frac++
			}
		case c == '.' && !dot:
			dot = true
		case c == 'e' || c == 'E':
			e, err := strconv.Atoi(string(b[i+1:]))
			if err != nil {
				return 0, 0, false
			}

			return mant, e - frac, seen
		default:
			return 0, 0, false
		}
	}

	return mant, -frac, seen
}

// scale128 returns m*10^k as a 128-bit value, ok=false if it exceeds 128 bits.
func scale128(m uint64, k int) (hi, lo uint64, ok bool) {
	if k > 38 { // 10^39 already overflows 128 bits for any m >= 1
		return 0, 0, m == 0
	}

	hi, lo = 0, m

	for ; k > 0; k-- {
		h, l := bits.Mul64(lo, 10)
		top, hl := bits.Mul64(hi, 10)
		if top != 0 {
			return 0, 0, false
		}

		hi = hl + h
		if hi < h {
			return 0, 0, false
		}

		lo = l
	}

	return hi, lo, true
}

// mod128 returns (hi:lo) mod d; reducing hi first keeps bits.Div64 in range.
func mod128(hi, lo, d uint64) uint64 {
	_, r := bits.Div64(hi%d, lo, d)
	return r
}
