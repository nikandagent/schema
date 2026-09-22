package schema

import (
	"errors"
	"strings"
	"testing"
)

func compileFlags(tb *testing.T, src string, fl Flags) *Schema {
	tb.Helper()

	s := &Schema{Flags: fl}
	if err := s.Compile([]byte(src)); err != nil {
		tb.Fatalf("compile %q: %v", src, err)
	}

	return s
}

func validFlags(tb *testing.T, src, data string, fl Flags) bool {
	tb.Helper()

	s := compileFlags(tb, src, fl)

	d, err := validate(s, []byte(data))
	if err != nil {
		tb.Fatalf("validate %q against %q: %v", data, src, err)
	}

	return len(d) == 0
}

func TestFormatAssertOff(tb *testing.T) {
	if !validFlags(tb, `{"format":"uuid"}`, `"nope"`, 0) {
		tb.Errorf("format asserted with the flag off")
	}

	if validFlags(tb, `{"format":"uuid"}`, `"nope"`, AssertStringFormat) {
		tb.Errorf("format not asserted with the flag on")
	}
}

func TestStringFormats(tb *testing.T) {
	for _, tc := range []struct {
		format, data string
		ok           bool
	}{
		{"uuid", `"9b2c1a34-5d6e-4f70-8a91-b2c3d4e5f607"`, true},
		{"uuid", `"9b2c1a34-5d6e-4f70-8a91-b2c3d4e5f60"`, false},
		{"uuid", `"9b2c1a345d6e4f708a91b2c3d4e5f607"`, false},
		{"uuid", `"9b2c1a34-5d6e-4f70-8a91-b2c3d4e5f60g"`, false},
		{"uuid", `42`, true},
		{"uuid", `null`, true},
		{"uuid", `["9b2c1a34-5d6e-4f70-8a91-b2c3d4e5f60g"]`, true},

		{"ipv4", `"1.2.3.4"`, true},
		{"ipv4", `"255.255.255.255"`, true},
		{"ipv4", `"01.2.3.4"`, false},
		{"ipv4", `"1.2.3"`, false},
		{"ipv4", `"::1"`, false},
		{"ipv4", `"1.2.3.4%eth0"`, false},

		{"ipv6", `"::1"`, true},
		{"ipv6", `"2001:db8::1"`, true},
		{"ipv6", `"::ffff:1.2.3.4"`, true},
		{"ipv6", `"1.2.3.4"`, false},
		{"ipv6", `"fe80::1%eth0"`, false},

		{"date", `"2026-08-12"`, true},
		{"date", `"2024-02-29"`, true},
		{"date", `"2023-02-29"`, false},
		{"date", `"1900-02-29"`, false},
		{"date", `"2000-02-29"`, true},
		{"date", `"2026-8-12"`, false},
		{"date", `"2026-13-01"`, false},

		{"time", `"10:05:00Z"`, true},
		{"time", `"10:05:00.123+02:00"`, true},
		{"time", `"10:05:00"`, false},
		{"time", `"10:05:00."`, false},
		{"time", `"24:05:00Z"`, false},
		// second 60 is a leap second, which happens only at 23:59:60 UTC
		{"time", `"23:59:60Z"`, true},
		{"time", `"15:59:60-08:00"`, true},
		{"time", `"22:59:60Z"`, false},
		{"time", `"23:59:60+01:00"`, false},

		{"date-time", `"2026-08-12T10:05:00Z"`, true},
		{"date-time", `"2026-08-12t10:05:00z"`, true},
		{"date-time", `"2026-08-12 10:05:00Z"`, false},
		{"date-time", `"2026-02-30T10:05:00Z"`, false},
		{"date-time", `"2026-08-12T10:05:00"`, false},
	} {
		src := `{"format":"` + tc.format + `"}`

		if got := validFlags(tb, src, tc.data, AssertStringFormat); got != tc.ok {
			tb.Errorf("%s %s: ok=%v, want %v", tc.format, tc.data, got, tc.ok)
		}
	}
}

func TestFormatEmail(tb *testing.T) {
	for _, tc := range []struct {
		data         string
		ok, okUseful bool
	}{
		{`"joe.bloggs@example.com"`, true, true},
		{`"a+tag@sub.example.co.uk"`, true, true},
		{`"joe@example.com"`, true, true},

		{`"joe bloggs@example.com"`, false, false},
		{`"joe..bloggs@example.com"`, false, false},
		{`".joe@example.com"`, false, false},
		{`"joe@"`, false, false},
		{`"@example.com"`, false, false},
		{`"joe@exa_mple.com"`, false, false},

		// RFC 5321 shapes nobody types on purpose
		{`"\"joe bloggs\"@example.com"`, true, false},
		{`"\"joe..bloggs\"@example.com"`, true, false},
		{`"\"joe@bloggs\"@example.com"`, true, false},
		{`"joe@[127.0.0.1]"`, true, false},
		{`"joe@[IPv6:::1]"`, true, false},
	} {
		if got := validFlags(tb, `{"format":"email"}`, tc.data, AssertStringFormat); got != tc.ok {
			tb.Errorf("email %s: ok=%v, want %v", tc.data, got, tc.ok)
		}

		if got := validFlags(tb, `{"format":"email"}`, tc.data, AssertStringFormat|AssertEmailUseful); got != tc.okUseful {
			tb.Errorf("email %s useful: ok=%v, want %v", tc.data, got, tc.okUseful)
		}
	}
}

func TestFormatUnasserted(tb *testing.T) {
	for _, name := range []string{
		"hostname", "duration", "uri", "regex", "idn-hostname", "idn-email",
		"iri", "iri-reference", "uri-template", "json-pointer",
		"relative-json-pointer", "nonsense",
	} {
		src := `{"format":"` + name + `"}`

		for _, data := range []string{`"nope"`, `""`, `42`} {
			if !validFlags(tb, src, data, AssertStringFormat) {
				tb.Errorf("%s %s: asserted, want inert", name, data)
			}
		}

		if got := string(compileFlags(tb, src, AssertStringFormat).Format(nil)); got != src {
			tb.Errorf("%s: format %q, want %q", name, got, src)
		}
	}
}

func TestFormatRoundTrip(tb *testing.T) {
	for _, name := range []string{"date-time", "date", "time", "email", "ipv4", "ipv6", "uuid"} {
		src := `{"format":"` + name + `"}`

		if got := string(compileFlags(tb, src, 0).Format(nil)); got != src {
			tb.Errorf("format %q: got %q", src, got)
		}
	}
}

func TestFormatCompileError(tb *testing.T) {
	for _, in := range []string{
		`{"format":123}`,
		`{"format":true}`,
		`{"format":["email"]}`,
		`{"format":{}}`,
	} {
		var s Schema

		err := s.Compile([]byte(in))
		if err == nil {
			tb.Errorf("compile %q: want error", in)
			continue
		}

		if !errors.Is(err, ErrKeyword) {
			tb.Errorf("compile %q: err %v, want Is(ErrKeyword)", in, err)
		}

		if d := AsDiag(err); len(d) != 1 || d[0].Code != MustBeString {
			tb.Errorf("compile %q: err %v, want MustBeString", in, err)
		}
	}
}

func TestFormatDiag(tb *testing.T) {
	s := compileFlags(tb, `{"properties":{"id":{"format":"uuid"}}}`, AssertStringFormat)

	data := []byte(`{"id":"nope"}`)

	d, err := validate(s, data)
	if err != nil {
		tb.Fatal(err)
	}

	if len(d) != 1 {
		tb.Fatalf("diags=%d, want 1: %+v", len(d), d)
	}

	if d[0].Code != FormatMismatch || d[0].Op.Op() != Format {
		tb.Errorf("diag=%+v, want FormatMismatch on Format", d[0])
	}

	if got := string(data[d[0].Off:d[0].End]); got != "nope" {
		tb.Errorf("diag span %q, want %q", got, "nope")
	}

	if !strings.Contains(d[0].Code.String(), "format") {
		tb.Errorf("message %q", d[0].Code.String())
	}
}
