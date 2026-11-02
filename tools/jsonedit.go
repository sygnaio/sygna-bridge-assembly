package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
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

// jsonScalar matches a JSON string, boolean or null value.
const jsonScalar = `("(?:[^"\\]|\\.)*"|true|false|null)`

// keyValueRe matches `"key": <scalar>` with any spacing around the colon.
func keyValueRe(key string) *regexp.Regexp {
	return regexp.MustCompile(regexp.QuoteMeta(quote(key)) + `\s*:\s*` + jsonScalar)
}

// setField replaces the value of the only `"key": <scalar>` in data, keeping
// every other byte (legacy files are not all single-line).
func setField(data []byte, key string, value any) ([]byte, error) {
	m := keyValueRe(key).FindAllSubmatchIndex(data, -1)
	if len(m) != 1 {
		return nil, fmt.Errorf("expected one %q field, found %d", key, len(m))
	}
	v, err := encodeJSON(value, "")
	if err != nil {
		return nil, err
	}
	return splice(data, m[0][2], m[0][3], v), nil
}

// fieldValue returns the decoded value of the only `"key": <scalar>` in data.
func fieldValue(data []byte, key string, v any) error {
	m := keyValueRe(key).FindAllSubmatch(data, -1)
	if len(m) != 1 {
		return fmt.Errorf("expected one %q field, found %d", key, len(m))
	}
	return json.Unmarshal(m[0][1], v)
}

// setCoingeckoID changes the coingecko_id of the mapping entry for currencyID
// (a UUID in mapping_v3.json, a legacy ID in mapping.json). found is false
// when the file has no entry for currencyID.
func setCoingeckoID(data []byte, currencyID, coingeckoID string) (out []byte, old string, found bool, err error) {
	ids := regexp.MustCompile(`"currency_id"\s*:\s*`+regexp.QuoteMeta(quote(currencyID))).FindAllIndex(data, -1)
	switch {
	case len(ids) == 0:
		return data, "", false, nil
	case len(ids) > 1:
		return nil, "", true, fmt.Errorf("%s has %d mapping entries", currencyID, len(ids))
	}
	rest := data[ids[0][1]:]
	end := bytes.IndexByte(rest, '}')
	m := keyValueRe("coingecko_id").FindSubmatchIndex(rest)
	if m == nil || end < 0 || m[0] > end {
		return nil, "", true, fmt.Errorf("mapping entry for %s has no coingecko_id after currency_id", currencyID)
	}
	if err := json.Unmarshal(rest[m[2]:m[3]], &old); err != nil {
		return nil, "", true, err
	}
	v, _ := encodeJSON(coingeckoID, "")
	off := ids[0][1]
	return splice(data, off+m[2], off+m[3], v), old, true, nil
}

// objectSpan returns the byte range of the `"key": { ... }` member of a
// top-level object whose values are flat objects (new_to_old_id_mapping.json).
func objectSpan(data []byte, key string) (start, end int, ok bool) {
	start = bytes.Index(data, []byte(quote(key)+": {"))
	if start < 0 {
		return 0, 0, false
	}
	brace := bytes.IndexByte(data[start:], '}')
	if brace < 0 {
		return 0, 0, false
	}
	return start, start + brace + 1, true
}

// removeEntry drops the `"key": "value"` line from a flat, one-entry-per-line
// object and fixes the comma on the line before it when it was the last one.
func removeEntry(obj []byte, key string) ([]byte, error) {
	lines := strings.Split(string(obj), "\n")
	idx := -1
	for i, l := range lines {
		if strings.HasPrefix(strings.TrimSpace(l), quote(key)+":") {
			if idx >= 0 {
				return nil, fmt.Errorf("key %q appears more than once", key)
			}
			idx = i
		}
	}
	if idx <= 0 || idx == len(lines)-1 {
		return nil, fmt.Errorf("key %q not found", key)
	}
	last := !strings.HasSuffix(strings.TrimRight(lines[idx], " \t\r"), ",")
	lines = append(lines[:idx], lines[idx+1:]...)
	if last && idx-1 > 0 {
		lines[idx-1] = strings.TrimSuffix(strings.TrimRight(lines[idx-1], " \t\r"), ",")
	}
	return []byte(strings.Join(lines, "\n")), nil
}

func splice(data []byte, start, end int, repl []byte) []byte {
	out := make([]byte, 0, len(data)-(end-start)+len(repl))
	out = append(out, data[:start]...)
	out = append(out, repl...)
	return append(out, data[end:]...)
}
