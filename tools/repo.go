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

// repo stages file changes in memory so a whole batch can be checked before
// anything is written, then writes (or deletes) them together.
type repo struct {
	root    string
	files   map[string][]byte // relative path → new content
	deleted map[string]bool   // relative path → to be removed
	order   []string
}

func newRepo(root string) *repo {
	return &repo{root: root, files: map[string][]byte{}, deleted: map[string]bool{}}
}

func (r *repo) path(rel string) string { return filepath.Join(r.root, rel) }

func (r *repo) read(rel string) ([]byte, error) {
	if r.deleted[rel] {
		return nil, &os.PathError{Op: "read", Path: rel, Err: os.ErrNotExist}
	}
	if b, ok := r.files[rel]; ok {
		return b, nil
	}
	return os.ReadFile(r.path(rel))
}

func (r *repo) stage(rel string, content []byte) {
	if _, ok := r.files[rel]; !ok {
		r.order = append(r.order, rel)
	}
	r.files[rel] = content
	delete(r.deleted, rel)
}

// stageDelete marks rel to be removed when the batch is written.
func (r *repo) stageDelete(rel string) {
	if _, ok := r.files[rel]; !ok && !r.deleted[rel] {
		r.order = append(r.order, rel)
	}
	delete(r.files, rel)
	r.deleted[rel] = true
}

// changeKind reports whether rel is added, modified or deleted by the batch.
func (r *repo) changeKind(rel string) string {
	switch _, err := os.Stat(r.path(rel)); {
	case r.deleted[rel]:
		return "delete"
	case os.IsNotExist(err):
		return "add"
	default:
		return "modify"
	}
}

func (r *repo) checkInsideRoot(rel string) error {
	root, err := filepath.Abs(r.root)
	if err != nil {
		return err
	}
	target, err := filepath.Abs(r.path(rel))
	if err != nil {
		return err
	}
	inside, err := filepath.Rel(root, target)
	if err != nil || inside == ".." || strings.HasPrefix(inside, ".."+string(filepath.Separator)) || filepath.IsAbs(inside) {
		return fmt.Errorf("refusing to write outside the repository: %s", rel)
	}
	return nil
}

// writeAll replaces each file atomically (temp file + rename, so an
// interrupted write never truncates a file), removes staged deletes and, if
// any step fails, restores the files already written or removed so the
// repository is never left half-updated.
func (r *repo) writeAll() error {
	type original struct {
		data   []byte
		exists bool
	}
	originals := map[string]original{}
	for _, rel := range r.order {
		data, err := os.ReadFile(r.path(rel))
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
	for _, rel := range r.order {
		var err error
		if r.deleted[rel] {
			err = os.Remove(r.path(rel))
		} else {
			err = writeFileAtomic(r.path(rel), r.files[rel])
		}
		if err != nil {
			var failed []string
			for _, w := range written {
				o := originals[w]
				var rerr error
				if o.exists {
					rerr = writeFileAtomic(r.path(w), o.data)
				} else {
					rerr = os.Remove(r.path(w))
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

func (r *repo) loadCoingeckoV3() (map[string]string, error) {
	data, err := r.read("coingecko_id/mapping_v3.json")
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

func (r *repo) loadStringMap(rel string) (map[string]string, error) {
	data, err := r.read(rel)
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

func (r *repo) loadLegacyIndex() (*legacyIndex, error) {
	entries, err := os.ReadDir(r.path("cryptocurrency"))
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

		data, err := os.ReadFile(filepath.Join(r.path("cryptocurrency"), n))
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
func (r *repo) loadV3Addresses() (map[string]string, error) {
	files, err := filepath.Glob(filepath.Join(r.path("cryptocurrency_v3"), "*.json"))
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
