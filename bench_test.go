package schema

import "testing"

var (
	benchSchema = []byte(`{
		"type": "object",
		"properties": {
			"id":    {"type": "integer"},
			"name":  {"type": "string", "minLength": 1},
			"email": {"type": "string"},
			"tags":  {"type": "array", "items": {"type": "string"}},
			"age":   {"type": "integer", "minimum": 0}
		},
		"required": ["id", "name"]
	}`)

	benchDoc = []byte(`{"id":1,"name":"Alice","email":"a@b.c","tags":["x","y","z"],"age":30}`)
)

func BenchmarkCompile(b *testing.B) {
	var s Schema

	b.ReportAllocs()

	for range b.N {
		if err := s.Compile(benchSchema); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkValidate(b *testing.B) {
	var s Schema
	if err := s.Compile(benchSchema); err != nil {
		b.Fatal(err)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		d, err := s.Validate(benchDoc)
		if err != nil {
			b.Fatal(err)
		}
		if len(d) != 0 {
			b.Fatalf("unexpected diags: %v", d)
		}
	}
}

func BenchmarkRewrite(b *testing.B) {
	var s Schema
	if err := s.Compile(benchSchema); err != nil {
		b.Fatal(err)
	}

	var w []byte

	b.ReportAllocs()
	b.ResetTimer()

	for range b.N {
		var err error
		w, _, err = s.Rewrite(w[:0], benchDoc)
		if err != nil {
			b.Fatal(err)
		}
	}
}
