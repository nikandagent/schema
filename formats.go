package schema

import (
	"bytes"
	"net/netip"
)

// strFormat is a "format" value we assert. A name outside this set never gets
// here: it stays a Raw annotation, which is what the spec asks for.
type strFormat int

const (
	formatDateTime strFormat = iota + 1
	formatDate
	formatTime
	formatEmail
	formatIPv4
	formatIPv6
	formatUUID
)

var formatNames = [...]string{
	formatDateTime: "date-time",
	formatDate:     "date",
	formatTime:     "time",
	formatEmail:    "email",
	formatIPv4:     "ipv4",
	formatIPv6:     "ipv6",
	formatUUID:     "uuid",
}

func formatOf(name []byte) strFormat {
	switch string(name) {
	case "date-time":
		return formatDateTime
	case "date":
		return formatDate
	case "time":
		return formatTime
	case "email":
		return formatEmail
	case "ipv4":
		return formatIPv4
	case "ipv6":
		return formatIPv6
	case "uuid":
		return formatUUID
	}

	return 0
}

func formatOK(s []byte, f strFormat, flags Flags) bool {
	switch f {
	case formatDateTime:
		return isDateTime(s)
	case formatDate:
		return isDate(s)
	case formatTime:
		return isTime(s)
	case formatEmail:
		return isEmail(s, flags.Is(AssertEmailUseful))
	case formatIPv4:
		return isIPv4(s)
	case formatIPv6:
		return isIPv6(s)
	case formatUUID:
		return isUUID(s)
	}

	return true
}

func isUUID(s []byte) bool {
	if len(s) != 36 {
		return false
	}

	for i, c := range s {
		switch i {
		case 8, 13, 18, 23:
			if c != '-' {
				return false
			}
		default:
			if !isHex(c) {
				return false
			}
		}
	}

	return true
}

// isIPv4 and isIPv6 lean on net/netip, which already spells out the grammar the
// RFCs do — leading zeros, "::" compression, 4-in-6. A zone (fe80::1%eth0) is an
// interface, not an address, so it is turned away.
func isIPv4(s []byte) bool {
	a, err := netip.ParseAddr(string(s))
	return err == nil && a.Is4() && a.Zone() == ""
}

func isIPv6(s []byte) bool {
	a, err := netip.ParseAddr(string(s))
	return err == nil && a.Is6() && a.Zone() == ""
}

// isDateTime is an RFC 3339 date-time: a full-date, T, and a full-time.
func isDateTime(s []byte) bool {
	if len(s) < 11 || s[10] != 'T' && s[10] != 't' {
		return false
	}

	return isDate(s[:10]) && isTime(s[11:])
}

// isDate is an RFC 3339 full-date, checked against the calendar.
func isDate(s []byte) bool {
	if len(s) != 10 || s[4] != '-' || s[7] != '-' {
		return false
	}

	y, ok := digits(s[:4])
	m, ok2 := digits(s[5:7])
	d, ok3 := digits(s[8:10])

	if !ok || !ok2 || !ok3 || m < 1 || m > 12 {
		return false
	}

	return d >= 1 && d <= monthDays(y, m)
}

// isTime is an RFC 3339 full-time. The offset is mandatory, and second 60 is a
// leap second, which only ever happens at 23:59:60 UTC — so the offset decides
// whether it is one, and 23:59:60+01:00 is not.
func isTime(s []byte) bool {
	if len(s) < 9 || s[2] != ':' || s[5] != ':' {
		return false
	}

	h, ok := digits(s[:2])
	m, ok2 := digits(s[3:5])
	sec, ok3 := digits(s[6:8])

	if !ok || !ok2 || !ok3 || h > 23 || m > 59 || sec > 60 {
		return false
	}

	i := 8

	if s[i] == '.' {
		i++
		st := i

		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}

		if i == st {
			return false
		}
	}

	off, ok := offsetMinutes(s[i:])
	if !ok {
		return false
	}

	const lastMinute = 23*60 + 59

	return sec != 60 || (h*60+m-off+2*24*60)%(24*60) == lastMinute
}

// offsetMinutes reads an RFC 3339 time-offset as minutes east of UTC.
func offsetMinutes(s []byte) (int, bool) {
	if len(s) == 1 {
		return 0, s[0] == 'Z' || s[0] == 'z'
	}

	if len(s) != 6 || s[0] != '+' && s[0] != '-' || s[3] != ':' {
		return 0, false
	}

	h, ok := digits(s[1:3])
	m, ok2 := digits(s[4:6])

	if !ok || !ok2 || h > 23 || m > 59 {
		return 0, false
	}

	if s[0] == '-' {
		return -(h*60 + m), true
	}

	return h*60 + m, true
}

// isHostname is an RFC 1123 host name: dot-separated labels of letters, digits
// and inner hyphens, each up to 63 bytes. The trailing dot that names the DNS
// root is not accepted; neither is any non-ASCII byte, which is idn-hostname.
func isHostname(s []byte) bool {
	if len(s) == 0 || len(s) > 253 {
		return false
	}

	n := 0 // bytes in the label so far

	for i, c := range s {
		switch {
		case c == '.':
			if n == 0 || s[i-1] == '-' {
				return false
			}

			n = 0

			continue
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		case c == '-' && n != 0:
		default:
			return false
		}

		n++

		if n > 63 {
			return false
		}
	}

	return n != 0 && s[len(s)-1] != '-'
}

// isEmail is an RFC 5321 addr-spec. A quoted local part ("joe bloggs"@example.com,
// which may hold spaces, doubled dots and even an @) and an address literal
// (joe@[127.0.0.1], joe@[IPv6:::1]) are unusual but legal, so they pass.
//
// useful drops both. They are the addresses nobody types on purpose, so an
// application taking one from a person is better served refusing them — but that
// is a choice about people, not about the spec, which is why it is a flag.
func isEmail(s []byte, useful bool) bool {
	if len(s) == 0 || len(s) > 254 {
		return false
	}

	n := dotAtom(s)
	if s[0] == '"' && !useful {
		n = quotedString(s)
	}

	if n < 1 || n > 64 || n >= len(s) || s[n] != '@' {
		return false
	}

	return emailDomain(s[n+1:], useful)
}

func emailDomain(s []byte, useful bool) bool {
	if len(s) > 2 && s[0] == '[' && s[len(s)-1] == ']' {
		if useful {
			return false
		}

		in := s[1 : len(s)-1]

		if bytes.HasPrefix(in, []byte("IPv6:")) {
			return isIPv6(in[5:])
		}

		return isIPv4(in)
	}

	return isHostname(s)
}

// dotAtom measures the atoms and dots starting s: no empty atom, so no leading,
// trailing or doubled dot.
func dotAtom(s []byte) int {
	i, atom := 0, false

	for ; i < len(s); i++ {
		switch {
		case isAtext(s[i]):
			atom = true
		case s[i] == '.' && atom:
			atom = false
		default:
			if !atom {
				return -1
			}

			return i
		}
	}

	if !atom {
		return -1
	}

	return i
}

// quotedString measures a quoted local part, escapes included.
func quotedString(s []byte) int {
	for i := 1; i < len(s); i++ {
		switch c := s[i]; {
		case c == '"':
			return i + 1
		case c == '\\':
			i++

			if i == len(s) || s[i] < ' ' || s[i] > '~' {
				return -1
			}
		case c < ' ' || c > '~':
			return -1
		}
	}

	return -1
}

func isAtext(c byte) bool {
	switch {
	case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9':
		return true
	}

	return bytes.IndexByte([]byte("!#$%&'*+-/=?^_`{|}~"), c) >= 0
}

func digits(s []byte) (int, bool) {
	v := 0

	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}

		v = v*10 + int(c-'0')
	}

	return v, true
}

func monthDays(y, m int) int {
	switch m {
	case 2:
		if y%4 == 0 && (y%100 != 0 || y%400 == 0) {
			return 29
		}

		return 28
	case 4, 6, 9, 11:
		return 30
	}

	return 31
}

func isHex(c byte) bool {
	return c >= '0' && c <= '9' || c >= 'a' && c <= 'f' || c >= 'A' && c <= 'F'
}
