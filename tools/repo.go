package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
)

type PlatformInfo struct {
	TokenAddress string `json:"token_address"`
}

// Platforms is a JSON object that keeps its key order, so rewriting an
// existing v3 file only shows the appended platform in the diff.
type Platforms struct {
	keys []string
	m    map[string]PlatformInfo
}

func (p *Platforms) UnmarshalJSON(data []byte) error {
	p.m = map[string]PlatformInfo{}
	p.keys = nil
	dec := json.NewDecoder(bytes.NewReader(data))
	if _, err := dec.Token(); err != nil {
		return err
	}
	for dec.More() {
		t, err := dec.Token()
		if err != nil {
			return err
		}
		key := t.(string)
		var info PlatformInfo
		if err := dec.Decode(&info); err != nil {
			return err
		}
		p.keys = append(p.keys, key)
		p.m[key] = info
	}
	_, err := dec.Token()
	return err
}

func (p Platforms) MarshalJSON() ([]byte, error) {
	var buf bytes.Buffer
	buf.WriteByte('{')
	for i, k := range p.keys {
		if i > 0 {
			buf.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		vb, err := json.Marshal(p.m[k])
		if err != nil {
			return nil, err
		}
		buf.Write(kb)
		buf.WriteByte(':')
		buf.Write(vb)
	}
	buf.WriteByte('}')
	return buf.Bytes(), nil
}

// Has reports whether key exists, ignoring case (existing data has e.g. "Kaia").
func (p *Platforms) Has(key string) bool {
	for _, k := range p.keys {
		if strings.EqualFold(k, key) {
			return true
		}
	}
	return false
}

func (p *Platforms) Add(key string, info PlatformInfo) {
	if p.m == nil {
		p.m = map[string]PlatformInfo{}
	}
	p.keys = append(p.keys, key)
	p.m[key] = info
}

type Currency struct {
	ID        string    `json:"id"`
	Symbol    string    `json:"symbol"`
	Name      string    `json:"name"`
	Platforms Platforms `json:"platforms"`
}

type CoingeckoMapping struct {
	CurrencyID  string `json:"currency_id"`
	CoingeckoID string `json:"coingecko_id"`
}

type LegacyPlatform struct {
	Name         string `json:"name"`
	Symbol       string `json:"symbol"`
	TokenAddress string `json:"token_address"`
	ID           string `json:"id"`
}

type LegacyCurrency struct {
	CurrencyID     string          `json:"currency_id"`
	CurrencyName   string          `json:"currency_name"`
	CurrencySymbol string          `json:"currency_symbol"`
	IsActive       bool            `json:"is_active"`
	AddrExtraInfo  []string        `json:"addr_extra_info"`
	Platform       *LegacyPlatform `json:"platform"`
}

func (a *adder) path(rel string) string { return filepath.Join(a.root, rel) }

func (a *adder) read(rel string) ([]byte, error) {
	if b, ok := a.files[rel]; ok {
		return b, nil
	}
	return os.ReadFile(a.path(rel))
}

func (a *adder) stage(rel string, content []byte) {
	if _, ok := a.files[rel]; !ok {
		a.order = append(a.order, rel)
	}
	a.files[rel] = content
}

func (a *adder) checkInsideRoot(rel string) error {
	root, err := filepath.Abs(a.root)
	if err != nil {
		return err
	}
	target, err := filepath.Abs(a.path(rel))
	if err != nil {
		return err
	}
	r, err := filepath.Rel(root, target)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) || filepath.IsAbs(r) {
		return fmt.Errorf("refusing to write outside the repository: %s", rel)
	}
	return nil
}

// writeAll replaces each file atomically (temp file + rename, so an
// interrupted write never truncates a file) and, if any write fails, restores
// the files already written so the repository is never left half-updated.
func (a *adder) writeAll() error {
	type original struct {
		data   []byte
		exists bool
	}
	originals := map[string]original{}
	for _, rel := range a.order {
		data, err := os.ReadFile(a.path(rel))
		switch {
		case err == nil:
			originals[rel] = original{data, true}
		case os.IsNotExist(err):
			originals[rel] = original{nil, false}
		default:
			return fmt.Errorf("failed to read %s: %v", rel, err)
		}
	}

	// Defer Ctrl-C until the batch (or its rollback) is finished.
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sig)

	var written []string
	for _, rel := range a.order {
		if err := writeFileAtomic(a.path(rel), a.files[rel]); err != nil {
			var failed []string
			for _, w := range written {
				o := originals[w]
				var rerr error
				if o.exists {
					rerr = writeFileAtomic(a.path(w), o.data)
				} else {
					rerr = os.Remove(a.path(w))
				}
				if rerr != nil {
					failed = append(failed, w)
				}
			}
			if len(failed) > 0 {
				return fmt.Errorf("failed to write %s: %v; rollback also failed — restore with: git checkout -- %s && git clean -f -- cryptocurrency", rel, err, strings.Join(failed, " "))
			}
			return fmt.Errorf("failed to write %s: %v; all changes rolled back, nothing was modified", rel, err)
		}
		written = append(written, rel)
		log.Printf("✓ %s", rel)
	}
	log.Println("✅ Done")
	return nil
}

func writeFileAtomic(path string, data []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".add_currency-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	mode := os.FileMode(0644)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	if err := os.Chmod(tmp.Name(), mode); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func (a *adder) loadCoingeckoV3() (map[string]string, error) {
	data, err := a.read("coingecko_id/mapping_v3.json")
	if err != nil {
		return nil, err
	}
	var list []CoingeckoMapping
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("failed to parse mapping_v3.json: %v", err)
	}
	m := map[string]string{}
	for _, e := range list {
		m[e.CurrencyID] = e.CoingeckoID
	}
	return m, nil
}

func (a *adder) loadStringMap(rel string) (map[string]string, error) {
	data, err := a.read(rel)
	if err != nil {
		return nil, err
	}
	var m map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %v", rel, err)
	}
	return m, nil
}

type legacyIndex struct {
	files     map[string]string                 // lowercased currency id (from filename) → filename
	platforms map[string]map[LegacyPlatform]int // sygna ID → name/symbol counts
}

func (a *adder) loadLegacyIndex() (*legacyIndex, error) {
	entries, err := os.ReadDir(a.path("cryptocurrency"))
	if err != nil {
		return nil, err
	}
	idx := &legacyIndex{files: map[string]string{}, platforms: map[string]map[LegacyPlatform]int{}}
	for _, e := range entries {
		n := e.Name()
		if !strings.HasSuffix(n, ".json") {
			continue
		}
		id := strings.TrimSuffix(n, ".json")
		if strings.HasPrefix(id, "sygna-") {
			id = "sygna:" + strings.TrimPrefix(id, "sygna-")
		}
		idx.files[strings.ToLower(id)] = n

		data, err := os.ReadFile(filepath.Join(a.path("cryptocurrency"), n))
		if err != nil {
			return nil, err
		}
		var c struct {
			Platform json.RawMessage `json:"platform"`
		}
		if json.Unmarshal(data, &c) != nil || len(c.Platform) == 0 || c.Platform[0] != '{' {
			continue
		}
		var p LegacyPlatform
		if json.Unmarshal(c.Platform, &p) != nil || p.ID == "" {
			continue
		}
		if idx.platforms[p.ID] == nil {
			idx.platforms[p.ID] = map[LegacyPlatform]int{}
		}
		idx.platforms[p.ID][LegacyPlatform{Name: p.Name, Symbol: p.Symbol}]++
	}
	return idx, nil
}

// platformFor returns the most common name/symbol used by tokens on sygnaID.
func (idx *legacyIndex) platformFor(sygnaID string) (LegacyPlatform, bool) {
	var best LegacyPlatform
	bestN := 0
	for p, n := range idx.platforms[sygnaID] {
		if n > bestN || (n == bestN && p.Name < best.Name) {
			best, bestN = p, n
		}
	}
	return best, bestN > 0
}

func (idx *legacyIndex) fileForID(id string) (string, bool) {
	f, ok := idx.files[strings.ToLower(id)]
	return f, ok
}

// loadV3Addresses indexes every token address in cryptocurrency_v3 as
// "<lowercased platform key>|<lowercased address>" → "SYMBOL (Name)".
func (a *adder) loadV3Addresses() (map[string]string, error) {
	files, err := filepath.Glob(filepath.Join(a.path("cryptocurrency_v3"), "*.json"))
	if err != nil {
		return nil, err
	}
	used := map[string]string{}
	for _, f := range files {
		data, err := os.ReadFile(f)
		if err != nil {
			return nil, err
		}
		var list []Currency
		if err := json.Unmarshal(data, &list); err != nil {
			var single Currency
			if json.Unmarshal(data, &single) != nil {
				return nil, fmt.Errorf("failed to parse %s: %v", f, err)
			}
			list = []Currency{single}
		}
		for _, c := range list {
			for _, k := range c.Platforms.keys {
				addr := strings.ToLower(c.Platforms.m[k].TokenAddress)
				if addr != "" && addr != "native" {
					used[strings.ToLower(k)+"|"+addr] = fmt.Sprintf("%s (%s)", c.Symbol, c.Name)
				}
			}
		}
	}
	return used, nil
}
