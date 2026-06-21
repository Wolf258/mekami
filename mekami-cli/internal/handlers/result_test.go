package handlers

import "testing"

func TestExtractData_Result(t *testing.T) {
	type payload struct {
		Name string
		N    int
	}
	r := AsResult("hello", payload{Name: "x", N: 7})
	got := ExtractData(r)
	p, ok := got.(payload)
	if !ok {
		t.Fatalf("ExtractData(Result) returned %T, want payload", got)
	}
	if p.Name != "x" || p.N != 7 {
		t.Fatalf("ExtractData(Result) = %+v, want {x 7}", p)
	}
}

func TestExtractData_ResultNilData(t *testing.T) {
	r := AsResult("only text", nil)
	if got := ExtractData(r); got != nil {
		t.Fatalf("ExtractData(Result{nil}) = %v, want nil", got)
	}
}

func TestExtractData_LegacyString(t *testing.T) {
	if got := ExtractData("plain"); got != "plain" {
		t.Fatalf("ExtractData(string) = %v, want plain", got)
	}
}

func TestExtractData_LegacyStruct(t *testing.T) {
	type s struct{ X int }
	v := s{X: 42}
	if got := ExtractData(v); got != v {
		t.Fatalf("ExtractData(struct) = %v, want %v", got, v)
	}
}

func TestTextView_Result(t *testing.T) {
	r := AsResult("hello world", []int{1, 2, 3})
	if got := TextView(r); got != "hello world" {
		t.Fatalf("TextView(Result) = %q, want %q", got, "hello world")
	}
}

func TestTextView_ResultEmpty(t *testing.T) {
	r := AsResult("", nil)
	if got := TextView(r); got != "" {
		t.Fatalf("TextView(Result{}) = %q, want empty", got)
	}
}

func TestTextView_LegacyString(t *testing.T) {
	if got := TextView("legacy"); got != "legacy" {
		t.Fatalf("TextView(string) = %q, want legacy", got)
	}
}

func TestTextView_LegacyStruct(t *testing.T) {
	type s struct{}
	if got := TextView(s{}); got != "" {
		t.Fatalf("TextView(struct) = %q, want empty (fallback)", got)
	}
}

func TestTextView_Nil(t *testing.T) {
	if got := TextView(nil); got != "" {
		t.Fatalf("TextView(nil) = %q, want empty", got)
	}
}
