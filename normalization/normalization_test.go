package normalization

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"reflect"
	"strings"
	"sync"
	"testing"
)

type sample struct {
	Name     string            `json:"name" unorm:"nfc" trim:"true"`
	Unicode  string            `json:"unicode" unorm:"nfc"`
	Trim     string            `json:"trim" trim:"true"`
	Optional *string           `json:"optional,omitempty" trim:"true" unorm:"nfc"`
	Secret   string            `json:"secret"`
	Map      map[string]string `json:"map"`
	Children []sample          `json:"children,omitempty"`
	Next     *sample           `json:"next,omitempty"`
}

func TestText(t *testing.T) {
	for _, test := range []struct{ name, input, want string }{
		{"composition", " e\u0301 ", "é"},
		{"unicode whitespace", "\u2003É\u00a0", "É"},
		{"internal whitespace", " A  B\tC ", "A  B\tC"},
		{"compatibility", " ① ", "①"},
		{"invisible", "\u200bA\u200b", "\u200bA\u200b"},
	} {
		t.Run(test.name, func(t *testing.T) {
			if got := Text(test.input, true, true); got != test.want {
				t.Fatalf("got %q, want %q", got, test.want)
			}
			if got := Text(Text(test.input, true, true), true, true); got != test.want {
				t.Fatalf("not idempotent: %q", got)
			}
		})
	}
}

func TestNormalize(t *testing.T) {
	optional := " e\u0301 "
	input := sample{Name: optional, Unicode: optional, Trim: optional, Optional: &optional,
		Secret: " e\u0301 ", Map: map[string]string{" e\u0301 ": " e\u0301 "},
		Children: []sample{{Name: " child "}}, Next: &sample{Name: " next "}}
	input.Next.Next = &input
	if err := Normalize(&input); err != nil {
		t.Fatal(err)
	}
	if input.Name != "é" || input.Unicode != " é " || input.Trim != "e\u0301" || *input.Optional != "é" {
		t.Fatalf("unexpected normalized values: %#v", input)
	}
	if input.Secret != " e\u0301 " || input.Map[" e\u0301 "] != " e\u0301 " {
		t.Fatal("untagged text changed")
	}
	if input.Children[0].Name != "child" || input.Next.Name != "next" {
		t.Fatal("nested values unchanged")
	}
	if input.Children[0].Optional != nil {
		t.Fatal("nil pointer allocated")
	}
	if err := Normalize(&input); err != nil {
		t.Fatal(err)
	}
	var empty *sample
	if err := Normalize(empty); err == nil {
		t.Fatal("expected typed nil rejection")
	}
	if err := Normalize(nil); err == nil {
		t.Fatal("expected nil rejection")
	}
	if err := Normalize(input); err == nil {
		t.Fatal("expected nonpointer rejection")
	}
}

func TestHasRules(t *testing.T) {
	tests := []struct {
		name    string
		typ     reflect.Type
		want    bool
		invalid bool
	}{
		{name: "tagged", typ: reflect.TypeFor[sample](), want: true},
		{name: "untagged", typ: reflect.TypeFor[struct{ Secret string }]()},
		{name: "map ignored", typ: reflect.TypeFor[map[string]sample]()},
		{name: "bad norm", typ: reflect.TypeFor[struct {
			Name string `unorm:"nfkc"`
		}](), invalid: true},
		{name: "bad trim", typ: reflect.TypeFor[struct {
			Name string `trim:"false"`
		}](), invalid: true},
		{name: "empty norm", typ: reflect.TypeFor[struct {
			Name string `unorm:""`
		}](), invalid: true},
		{name: "number", typ: reflect.TypeFor[struct {
			Name int `trim:"true"`
		}](), invalid: true},
		{name: "slice", typ: reflect.TypeFor[struct {
			Name []string `trim:"true"`
		}](), invalid: true},
		{name: "private", typ: reflect.TypeFor[struct {
			name string `trim:"true"`
		}](), invalid: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := HasRules(test.typ)
			if (err != nil) != test.invalid || got != test.want {
				t.Fatalf("got %v, %v", got, err)
			}
		})
	}
}

func TestNormalizeJSON(t *testing.T) {
	data := []byte(`{"name":" e\u0301 ","unicode":" e\u0301 ","trim":" e\u0301 ","optional":null,"secret":" e\u0301 ","map":{" e\u0301 ":" e\u0301 "},"children":[{"name":" child "}],"large":900719925474099312345,"decimal":1.2300e+99}`)
	result, err := NormalizeJSON(data, reflect.TypeFor[sample]())
	if err != nil {
		t.Fatal(err)
	}
	var value sample
	if err := json.Unmarshal(result, &value); err != nil {
		t.Fatal(err)
	}
	if value.Name != "é" || value.Unicode != " é " || value.Trim != "e\u0301" || value.Optional != nil || value.Children[0].Name != "child" {
		t.Fatalf("unexpected result %s", result)
	}
	if value.Secret != " e\u0301 " || value.Map[" e\u0301 "] != " e\u0301 " {
		t.Fatal("untagged values changed")
	}
	var fields map[string]jsontext.Value
	if err := json.Unmarshal(result, &fields); err != nil {
		t.Fatal(err)
	}
	if string(fields["large"]) != "900719925474099312345" || string(fields["decimal"]) != "1.2300e+99" {
		t.Fatal("numbers changed")
	}
	for _, test := range []struct{ name, input string }{
		{"omitted", `{}`}, {"empty", `{"optional":""}`}, {"wrong type", `{"name":42}`}, {"null", `null`},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := NormalizeJSON([]byte(test.input), reflect.TypeFor[sample]())
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != test.input {
				t.Fatalf("got %s", got)
			}
		})
	}
	for _, input := range []string{"[]", "null"} {
		got, err := NormalizeJSON([]byte(input), reflect.TypeFor[[]sample]())
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != input {
			t.Fatalf("array %s became %s", input, got)
		}
	}
	for _, input := range []string{`{"name":`, `{"name":"a","name":"b"}`, `{} {}`, `{"unknown":{"duplicate":1,"duplicate":2}}`} {
		if _, err := NormalizeJSON([]byte(input), reflect.TypeFor[sample]()); err == nil {
			t.Fatalf("accepted malformed JSON %s", input)
		}
	}
}

func TestNormalizeNestedAndConcurrent(t *testing.T) {
	type embedded struct {
		Value string `json:"value" trim:"true"`
	}
	type envelope struct {
		Embedded embedded   `json:",inline"`
		Array    [1]*sample `json:"array"`
	}
	input := envelope{Embedded: embedded{Value: " a "}, Array: [1]*sample{{Name: " b "}}}
	if err := Normalize(&input); err != nil {
		t.Fatal(err)
	}
	if input.Embedded.Value != "a" || input.Array[0].Name != "b" {
		t.Fatal("nested values unchanged")
	}
	got, err := NormalizeJSON([]byte(`{"value":" a ","array":[{"name":" b "}]}`), reflect.TypeFor[envelope]())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(got), " a ") || strings.Contains(string(got), " b ") {
		t.Fatalf("not normalized: %s", got)
	}
	var workers sync.WaitGroup
	for range 20 {
		workers.Go(func() {
			value := sample{Name: " e\u0301 "}
			if err := Normalize(&value); err != nil {
				t.Error(err)
			}
			if value.Name != "é" {
				t.Errorf("unexpected %q", value.Name)
			}
		})
	}
	workers.Wait()
}

func TestRecursiveInlineRejected(t *testing.T) {
	type RecursiveEmbedded struct {
		*RecursiveEmbedded
		Name string `json:"name" trim:"true"`
	}
	typ := reflect.TypeFor[RecursiveEmbedded]()
	if _, err := HasRules(typ); err == nil {
		t.Fatal("accepted recursive inline fields")
	}
	if _, err := NormalizeJSON([]byte(`{"name":" a "}`), typ); err == nil {
		t.Fatal("accepted recursive inline JSON")
	}
	value := RecursiveEmbedded{Name: " a "}
	if err := Normalize(&value); err == nil {
		t.Fatal("accepted recursive inline DTO")
	}
	if value.Name != " a " {
		t.Fatal("modified input before rejecting metadata")
	}
}

func TestNormalizeReplacesAliasedStringPointers(t *testing.T) {
	type row struct {
		Name     string   `trim:"true" unorm:"nfc"`
		Optional *string  `trim:"true" unorm:"nfc"`
		Nested   **string `trim:"true" unorm:"nfc"`
		InnerNil **string `trim:"true"`
		Nil      *string  `trim:"true"`
	}
	text := " e\u0301 "
	pointer := &text
	var nilPointer *string
	original := row{Name: text, Optional: pointer, Nested: &pointer, InnerNil: &nilPointer}
	normalized := original
	if err := Normalize(&normalized); err != nil {
		t.Fatal(err)
	}
	if normalized.Name != "é" || *normalized.Optional != "é" || **normalized.Nested != "é" {
		t.Fatalf("unexpected normalized row: %#v", normalized)
	}
	if original.Name != text || *original.Optional != " e\u0301 " || **original.Nested != " e\u0301 " {
		t.Fatal("normalization changed original row")
	}
	if normalized.Optional == original.Optional || normalized.Nested == original.Nested || *normalized.Nested == *original.Nested {
		t.Fatal("normalized pointers still alias originals")
	}
	if normalized.Nil != nil || normalized.InnerNil == nil || *normalized.InnerNil != nil {
		t.Fatal("nil pointer shape changed")
	}
}

func TestNormalizeLengthConstraints(t *testing.T) {
	type input struct {
		Name     string  `json:"name" trim:"true" unorm:"nfc" minLength:"1" maxLength:"2"`
		Optional *string `json:"optional" trim:"true" minLength:"1"`
	}
	for _, test := range []struct {
		name, text, want string
		invalid          bool
	}{
		{name: "empty", text: "  ", invalid: true},
		{name: "unicode rune length", text: " e\u0301 ", want: "é"},
		{name: "maximum", text: " café ", invalid: true},
		{name: "two runes", text: " 界é ", want: "界é"},
	} {
		t.Run(test.name, func(t *testing.T) {
			value := input{Name: test.text}
			err := Normalize(&value)
			if (err != nil) != test.invalid {
				t.Fatalf("unexpected error %v", err)
			}
			if err != nil && !strings.Contains(err.Error(), "name:") {
				t.Fatalf("missing field name: %v", err)
			}
			if !test.invalid && value.Name != test.want {
				t.Fatalf("got %q", value.Name)
			}
			if value.Optional != nil {
				t.Fatal("allocated optional pointer")
			}
		})
	}
	got, err := NormalizeJSON([]byte(`{"name":"   "}`), reflect.TypeFor[input]())
	if err != nil || string(got) != `{"name":""}` {
		t.Fatalf("JSON must leave length validation to Huma: %s, %v", got, err)
	}
	for _, typ := range []reflect.Type{
		reflect.TypeFor[struct {
			Name string `trim:"true" minLength:"-1"`
		}](),
		reflect.TypeFor[struct {
			Name string `trim:"true" maxLength:"no"`
		}](),
		reflect.TypeFor[struct {
			Name string `trim:"true" minLength:"2" maxLength:"1"`
		}](),
	} {
		if _, err := HasRules(typ); err == nil {
			t.Fatal("accepted invalid length metadata")
		}
	}
}
