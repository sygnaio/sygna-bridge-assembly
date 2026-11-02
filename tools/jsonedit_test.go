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
