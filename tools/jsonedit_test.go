package main

import (
	"strings"
	"testing"
)

func TestAppendToContainer(t *testing.T) {
	tests := []struct {
		name, in, entry string
		closing         byte
		want            string
	}{
		{"array without trailing newline", "[\n  1,\n  2\n]", "  3", ']', "[\n  1,\n  2,\n  3\n]"},
		{"object keeps trailing newline", "{\n  \"a\": 1\n}\n", "  \"b\": 2", '}', "{\n  \"a\": 1,\n  \"b\": 2\n}\n"},
		{"empty array gets no comma", "[\n]", "  1", ']', "[\n  1\n]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := appendToContainer([]byte(tt.in), tt.closing, "", tt.entry)
			if err != nil {
				t.Fatal(err)
			}
			if string(got) != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestAppendToContainerRejectsWrongClosing(t *testing.T) {
	if _, err := appendToContainer([]byte("[\n  1\n]"), '}', "", "  2"); err == nil || !strings.Contains(err.Error(), "expected content to end with") {
		t.Errorf("err = %v, want a closing-bracket error", err)
	}
}

func TestDuplicateKeys(t *testing.T) {
	got := duplicateKeys([]byte(`{"a":{"x":1,"x":2},"b":[{"y":1,"y":2}],"a":3}`))
	want := []string{"$.a.x", "$.b[0].y", "$.a"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestSetFieldKeepsFormatting(t *testing.T) {
	compact := `{"currency_id":"sygna:0x1","currency_name":"Old","is_active":true,"platform":{"name":"Ethereum"}}`
	got, err := setField([]byte(compact), "currency_name", "New <&>")
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"currency_id":"sygna:0x1","currency_name":"New <&>","is_active":true,"platform":{"name":"Ethereum"}}`; string(got) != want {
		t.Errorf("got %s", got)
	}
	pretty := "{\n  \"currency_id\": \"sygna:0x1\",\n  \"is_active\": true\n}"
	got, err = setField([]byte(pretty), "is_active", false)
	if err != nil {
		t.Fatal(err)
	}
	if want := "{\n  \"currency_id\": \"sygna:0x1\",\n  \"is_active\": false\n}"; string(got) != want {
		t.Errorf("got %q", got)
	}
	if _, err := setField([]byte(compact), "missing", 1); err == nil {
		t.Error("want an error for a missing field")
	}
}

func TestSetCoingeckoID(t *testing.T) {
	in := "[\n    {\n        \"currency_id\": \"a\",\n        \"coingecko_id\": \"x\"\n    },\n    {\n        \"currency_id\": \"b\",\n        \"coingecko_id\": \"y\"\n    }\n]"
	got, old, found, err := setCoingeckoID([]byte(in), "b", "z")
	if err != nil || !found || old != "y" {
		t.Fatalf("old=%q found=%v err=%v", old, found, err)
	}
	if want := strings.Replace(in, `"y"`, `"z"`, 1); string(got) != want {
		t.Errorf("got %q", got)
	}
	if _, _, found, err := setCoingeckoID([]byte(in), "c", "z"); found || err != nil {
		t.Errorf("missing entry: found=%v err=%v", found, err)
	}
	dup := strings.Replace(in, `"currency_id": "a"`, `"currency_id": "b"`, 1)
	if _, _, _, err := setCoingeckoID([]byte(dup), "b", "z"); err == nil {
		t.Error("want an error for a duplicated entry")
	}
}

func TestRemoveEntry(t *testing.T) {
	obj := "\"u\": {\n    \"a\": \"1\",\n    \"b\": \"2\",\n    \"c\": \"3\"\n  }"
	tests := []struct{ key, want string }{
		{"a", "\"u\": {\n    \"b\": \"2\",\n    \"c\": \"3\"\n  }"},
		{"b", "\"u\": {\n    \"a\": \"1\",\n    \"c\": \"3\"\n  }"},
		{"c", "\"u\": {\n    \"a\": \"1\",\n    \"b\": \"2\"\n  }"},
	}
	for _, tt := range tests {
		got, err := removeEntry([]byte(obj), tt.key)
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != tt.want {
			t.Errorf("remove %s: got %q", tt.key, got)
		}
	}
	if _, err := removeEntry([]byte(obj), "d"); err == nil {
		t.Error("want an error for a missing key")
	}
}

func TestObjectSpan(t *testing.T) {
	data := []byte("{\n  \"u1\": {\n    \"a\": \"1\"\n  },\n  \"u2\": {\n    \"b\": \"2\"\n  }\n}")
	s, e, ok := objectSpan(data, "u2")
	if !ok || string(data[s:e]) != "\"u2\": {\n    \"b\": \"2\"\n  }" {
		t.Errorf("got %q ok=%v", data[s:e], ok)
	}
	if _, _, ok := objectSpan(data, "u3"); ok {
		t.Error("want ok=false for a missing key")
	}
}
