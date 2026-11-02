package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

func appendToContainer(data []byte, closing byte, closingIndent, entry string) ([]byte, error) {
	trimmed := bytes.TrimRight(data, " \t\r\n")
	trailing := data[len(trimmed):]
	if len(trimmed) == 0 || trimmed[len(trimmed)-1] != closing {
		return nil, fmt.Errorf("expected content to end with %q", closing)
	}
	body := bytes.TrimRight(trimmed[:len(trimmed)-1], " \t\r\n")
	if len(body) == 0 {
		return nil, fmt.Errorf("expected an enclosing array or object")
	}
	var out bytes.Buffer
	out.Write(body)
	if last := body[len(body)-1]; last != '[' && last != '{' {
		out.WriteByte(',')
	}
	out.WriteString("\n" + entry + "\n" + closingIndent)
	out.WriteByte(closing)
	out.Write(trailing)
	return out.Bytes(), nil
}

// encodeJSON marshals without HTML escaping and without a trailing newline.
func encodeJSON(v any, indent string) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if indent != "" {
		enc.SetIndent("", indent)
	}
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// indentJSON renders v as an indented block whose every line starts with prefix.
func indentJSON(v any, prefix, indent string) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent(prefix, indent)
	_ = enc.Encode(v)
	return prefix + strings.TrimRight(buf.String(), "\n")
}

func quote(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}

// duplicateKeys returns the paths of object keys that appear more than once
// in the same object. encoding/json silently keeps the last one.
func duplicateKeys(data []byte) []string {
	var dups []string
	dec := json.NewDecoder(bytes.NewReader(data))
	var walk func(path string) error
	walk = func(path string) error {
		t, err := dec.Token()
		if err != nil {
			return err
		}
		switch t {
		case json.Delim('{'):
			seen := map[string]bool{}
			for dec.More() {
				kt, err := dec.Token()
				if err != nil {
					return err
				}
				k := kt.(string)
				if seen[k] {
					dups = append(dups, path+"."+k)
				}
				seen[k] = true
				if err := walk(path + "." + k); err != nil {
					return err
				}
			}
			_, err = dec.Token()
			return err
		case json.Delim('['):
			for i := 0; dec.More(); i++ {
				if err := walk(fmt.Sprintf("%s[%d]", path, i)); err != nil {
					return err
				}
			}
			_, err = dec.Token()
			return err
		}
		return nil
	}
	_ = walk("$")
	return dups
}
