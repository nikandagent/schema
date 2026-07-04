package schema

import "testing"

func TestIsMultiple(tb *testing.T) {
	for _, tc := range []struct {
		value, div string
		ok, exact  bool
	}{
		{"10", "2", true, true},
		{"10", "3", false, true},
		{"0.0075", "0.0001", true, true}, // binary float would miss this
		{"4.5", "1.5", true, true},
		{"4.5", "2", false, true},
		{"5", "1e-8", true, true}, // 5 == 5e8 * 1e-8
		{"1e-8", "1e-8", true, true},
		{"0", "0.3", true, true},                           // zero is a multiple of anything
		{"1", "0", false, false},                           // divisor 0 -> fall back
		{"100000000000000000000000000", "1", false, false}, // mantissa over 64 bits -> fall back
	} {
		ok, exact := isMultiple([]byte(tc.value), []byte(tc.div))
		if ok != tc.ok || exact != tc.exact {
			tb.Errorf("isMultiple(%s, %s) = (%v,%v), want (%v,%v)",
				tc.value, tc.div, ok, exact, tc.ok, tc.exact)
		}
	}
}
