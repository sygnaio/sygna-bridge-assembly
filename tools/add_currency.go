package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"syscall"

	"github.com/google/uuid"
)

// knownSygnaIDs pins the legacy Sygna ID for common chains. Historical data is
// inconsistent for some keys (e.g. binance-smart-chain was sometimes mapped to
// the old Binance Chain ID 0x800002ca), so these take precedence over lookups.
var knownSygnaIDs = map[string]string{
	"ethereum":            "sygna:0x8000003c",
	"binance-smart-chain": "sygna:0x8000232e",
	"tron":                "sygna:0x800000c3",
	"solana":              "sygna:0x800001f5",
	"polygon":             "sygna:0x800003c6",
	"optimism":            "sygna:0x80000266",
	"avalanche-c-chain":   "sygna:0x8000232d",
	"arbitrum":            "sygna:0x80002329",
	"cardano":             "sygna:0x80000717",
	"eos":                 "sygna:0x800000c2",
	"neo":                 "sygna:0x80000378",
	"stellar":             "sygna:0x80000094",
}

// evmPlatforms are v3 platform keys whose token addresses must be EVM addresses.
var evmPlatforms = map[string]bool{
	"ethereum":            true,
	"binance-smart-chain": true,
	"polygon":             true,
	"optimism":            true,
	"avalanche-c-chain":   true,
	"arbitrum":            true,
	"huobi-eco-chain":     true,
	"base":                true,
	"hyperevm":            true,
	"abstract-chain":      true,
}

var (
	evmAddressRe  = regexp.MustCompile(`^0[xX][0-9a-fA-F]{40}$`)
	symbolRe      = regexp.MustCompile(`^[A-Z0-9$][A-Z0-9$._-]*$`)
	platformKeyRe = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]*$`)
)

// validateAddress rejects values that could escape the data directory when
// used in a filename. Inputs come from ticket text, so treat them as untrusted.
func validateAddress(key, addr string) error {
	if addr == "" {
		return fmt.Errorf("missing address for platform %q; use -platform %s:ADDRESS or %s:native", key, key, key)
	}
	if strings.ContainsAny(addr, "/\\") || strings.Contains(addr, "..") {
		return fmt.Errorf("invalid address %q for %s: must not contain '/', '\\' or '..'", addr, key)
	}
	for _, r := range addr {
		if r <= ' ' || r == 0x7f {
			return fmt.Errorf("invalid address %q for %s: contains whitespace or control characters", addr, key)
		}
	}
	if evmPlatforms[key] && !evmAddressRe.MatchString(addr) {
		return fmt.Errorf("invalid address %q for %s: expected 0x followed by 40 hex characters", addr, key)
	}
	return nil
}

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

// repeated collects a flag that may be passed multiple times.
type repeated []string

func (r *repeated) String() string     { return strings.Join(*r, ",") }
func (r *repeated) Set(v string) error { *r = append(*r, v); return nil }

type platformSpec struct {
	key       string
	v3Address string // lowercased, or "native"
	legacy    *legacyPlan
}

type legacyPlan struct {
	sygnaID  string
	address  string // original case except EVM (lowercased)
	id       string // <sygnaID>.<address>
	platform LegacyPlatform
	filename string
}

func main() {
	var platformFlags, sygnaIDFlags repeated
	symbol := flag.String("symbol", "", "Currency symbol (required, e.g. BTC, USDT)")
	name := flag.String("name", "", "Currency name (required, e.g. Bitcoin, Tether)")
	flag.Var(&platformFlags, "platform", "Platform as KEY:ADDRESS or KEY:native (repeatable). KEY alone only together with --token-address")
	tokenAddress := flag.String("token-address", "", "Token address (or native) when a single -platform is given without :ADDRESS")
	coingeckoID := flag.String("coingecko-id", "", "CoinGecko ID (required for a new currency)")
	allowSharedCG := flag.Bool("allow-shared-coingecko-id", false, "Allow a new currency to use a CoinGecko ID already mapped to another currency")
	flag.Var(&sygnaIDFlags, "sygna-id", "Override legacy Sygna ID as KEY=sygna:... (repeatable)")
	fixedUUID := flag.String("uuid", "", "Use this UUID for a new currency instead of generating one")
	root := flag.String("root", "..", "Repository root")
	dryRun := flag.Bool("dry-run", false, "Print the planned changes without writing")
	flag.Parse()

	if *symbol == "" || *name == "" || len(platformFlags) == 0 {
		fmt.Println("Usage: go run add_currency.go -symbol SYMBOL -name NAME -platform KEY:ADDRESS [-platform KEY:ADDRESS ...] [--coingecko-id ID] [-dry-run]")
		fmt.Println("\nExamples:")
		fmt.Println(`go run add_currency.go -symbol NIGHT -name Midnight --coingecko-id midnight-3 -platform binance-smart-chain:0xfe930c2d63aed9b82fc4dbc801920dd2c1a3224f -platform cardano:0691b2fecca1ac4f53cb6dfb00b7013e561d1f34403b957cbb5af1fa4e49474854`)
		fmt.Println("go run add_currency.go -symbol USDT -name Tether -platform polygon --token-address 0xc2132d05d31c914a87c6611c10748aeb04b58e8f")
		os.Exit(1)
	}

	normSymbol := strings.ToUpper(strings.TrimSpace(*symbol))
	if !symbolRe.MatchString(normSymbol) || strings.Contains(normSymbol, "..") {
		log.Fatalf("Error: invalid -symbol %q: only A-Z, 0-9, '$', '.', '_' and '-' are allowed", *symbol)
	}
	overrides := map[string]string{}
	for _, s := range sygnaIDFlags {
		k, v, ok := strings.Cut(s, "=")
		if !ok || !strings.HasPrefix(v, "sygna:") {
			log.Fatalf("Error: invalid -sygna-id %q, expected KEY=sygna:...", s)
		}
		overrides[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
	}

	a := &adder{
		root:          *root,
		symbol:        normSymbol,
		name:          strings.TrimSpace(*name),
		coingeckoID:   strings.ToLower(strings.TrimSpace(*coingeckoID)),
		uuid:          strings.TrimSpace(*fixedUUID),
		overrides:     overrides,
		allowSharedCG: *allowSharedCG,
		files:         map[string][]byte{},
	}
	for _, p := range platformFlags {
		key, addr, hasAddr := strings.Cut(p, ":")
		if !hasAddr {
			if len(platformFlags) > 1 && *tokenAddress != "" {
				log.Fatalf("Error: --token-address is ambiguous with multiple -platform flags; use -platform KEY:ADDRESS")
			}
			addr = *tokenAddress
		}
		key = strings.ToLower(strings.TrimSpace(key))
		if !platformKeyRe.MatchString(key) {
			log.Fatalf("Error: invalid platform key %q", key)
		}
		a.rawPlatforms = append(a.rawPlatforms, [2]string{key, strings.TrimSpace(addr)})
	}

	if err := a.run(*dryRun); err != nil {
		log.Fatalf("Error: %v", err)
	}
}

type adder struct {
	root          string
	symbol        string
	name          string
	coingeckoID   string
	uuid          string
	overrides     map[string]string
	allowSharedCG bool
	rawPlatforms  [][2]string

	isNew     bool
	addCGv3   bool // append a mapping_v3.json entry (new currency, or existing one missing it)
	platforms []platformSpec
	files     map[string][]byte // relative path → new content
	order     []string
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

func (a *adder) run(dryRun bool) error {
	v3File := fmt.Sprintf("cryptocurrency_v3/%s.json", a.symbol)
	var currencies []Currency
	if data, err := os.ReadFile(a.path(v3File)); err == nil {
		if err := json.Unmarshal(data, &currencies); err != nil {
			return fmt.Errorf("failed to parse %s: %v", v3File, err)
		}
	} else if !os.IsNotExist(err) {
		return err
	}

	var target *Currency
	for i := range currencies {
		if currencies[i].Name == a.name {
			target = &currencies[i]
			break
		}
		if strings.EqualFold(currencies[i].Name, a.name) {
			return fmt.Errorf("%s already has currency %q (differs only in case from %q); use the exact existing name", v3File, currencies[i].Name, a.name)
		}
	}

	cgV3, err := a.loadCoingeckoV3()
	if err != nil {
		return err
	}

	if target == nil {
		a.isNew = true
		a.addCGv3 = true
		if a.coingeckoID == "" {
			return fmt.Errorf("--coingecko-id is required when creating a new currency")
		}
		if !a.allowSharedCG {
			for id, cg := range cgV3 {
				if cg == a.coingeckoID {
					return fmt.Errorf("CoinGecko ID %q is already mapped to currency %s; if this is really a separate currency, pass -allow-shared-coingecko-id", cg, id)
				}
			}
		}
		if a.uuid == "" {
			a.uuid = uuid.New().String()
		} else if _, err := uuid.Parse(a.uuid); err != nil {
			return fmt.Errorf("invalid -uuid: %v", err)
		}
		currencies = append(currencies, Currency{ID: a.uuid, Symbol: a.symbol, Name: a.name})
		target = &currencies[len(currencies)-1]
		log.Printf("Creating new currency %q (%s) with UUID %s", a.name, a.symbol, a.uuid)
	} else {
		a.uuid = target.ID
		existing := cgV3[a.uuid]
		switch {
		case a.coingeckoID == "" && existing == "":
			return fmt.Errorf("no CoinGecko ID found for %s in mapping_v3.json; pass --coingecko-id", a.uuid)
		case a.coingeckoID == "":
			a.coingeckoID = existing
		case existing == "":
			a.addCGv3 = true
		case existing != a.coingeckoID:
			return fmt.Errorf("--coingecko-id %q differs from existing mapping %q for %s", a.coingeckoID, existing, a.uuid)
		}
		log.Printf("Adding platforms to existing currency %q (UUID %s)", a.name, a.uuid)
	}

	if err := a.plan(target); err != nil {
		return err
	}

	for _, p := range a.platforms {
		target.Platforms.Add(p.key, PlatformInfo{TokenAddress: p.v3Address})
	}
	v3Data, err := encodeJSON(currencies, "  ")
	if err != nil {
		return err
	}
	a.stage(v3File, v3Data)

	if err := a.stageMappings(cgV3); err != nil {
		return err
	}
	if err := a.verify(); err != nil {
		return fmt.Errorf("self-check failed, nothing written: %v", err)
	}

	for _, rel := range a.order {
		if err := a.checkInsideRoot(rel); err != nil {
			return err
		}
	}

	if dryRun {
		a.printPlan()
		return nil
	}
	return a.writeAll()
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

// plan validates every requested platform and resolves its legacy details
// before anything is written.
func (a *adder) plan(target *Currency) error {
	legacyIndex, err := a.loadLegacyIndex()
	if err != nil {
		return err
	}
	oldToNew, err := a.loadStringMap("id_mapping/old_to_new_id_mapping.json")
	if err != nil {
		return err
	}
	existingIDs := map[string]string{}
	for k := range oldToNew {
		existingIDs[strings.ToLower(k)] = k
	}
	var newToOld map[string]map[string]string
	if data, err := a.read("id_mapping/new_to_old_id_mapping.json"); err != nil {
		return err
	} else if err := json.Unmarshal(data, &newToOld); err != nil {
		return fmt.Errorf("failed to parse id_mapping/new_to_old_id_mapping.json: %v", err)
	}
	usedAddresses, err := a.loadV3Addresses()
	if err != nil {
		return err
	}

	seen := map[string]bool{}
	for _, rp := range a.rawPlatforms {
		key, addr := rp[0], rp[1]
		if key == "" {
			return fmt.Errorf("empty platform key")
		}
		if seen[key] {
			return fmt.Errorf("platform %q given twice", key)
		}
		seen[key] = true
		if target.Platforms.Has(key) {
			return fmt.Errorf("platform %q already exists on %q", key, a.name)
		}
		for k := range newToOld[target.ID] {
			if strings.EqualFold(k, key) {
				return fmt.Errorf("platform %q already exists for %s in new_to_old_id_mapping.json", k, target.ID)
			}
		}

		if strings.EqualFold(addr, "native") {
			log.Printf("⚠️ %s is native: v3 only, legacy files must be added manually", key)
			a.platforms = append(a.platforms, platformSpec{key: key, v3Address: "native"})
			continue
		}
		if err := validateAddress(key, addr); err != nil {
			return err
		}
		if evmAddressRe.MatchString(addr) {
			addr = strings.ToLower(addr)
		}
		spec := platformSpec{key: key, v3Address: strings.ToLower(addr)}
		if owner, dup := usedAddresses[key+"|"+spec.v3Address]; dup {
			return fmt.Errorf("address %s on %s is already used by %s", addr, key, owner)
		}

		sygnaID, err := a.resolveSygnaID(key)
		if err != nil {
			return err
		}
		plat, ok := legacyIndex.platformFor(sygnaID)
		if !ok {
			return fmt.Errorf("no existing legacy token on %s (%s) to copy platform name/symbol from; add this platform manually", key, sygnaID)
		}
		legacyAddr := addr
		lp := &legacyPlan{
			sygnaID:  sygnaID,
			address:  legacyAddr,
			id:       sygnaID + "." + legacyAddr,
			platform: LegacyPlatform{Name: plat.Name, Symbol: plat.Symbol, TokenAddress: legacyAddr, ID: sygnaID},
		}
		lp.filename = "cryptocurrency/sygna-" + strings.TrimPrefix(lp.id, "sygna:") + ".json"

		if orig, dup := existingIDs[strings.ToLower(lp.id)]; dup {
			return fmt.Errorf("legacy ID %s already mapped in old_to_new_id_mapping.json", orig)
		}
		if f, dup := legacyIndex.fileForID(lp.id); dup {
			return fmt.Errorf("legacy file already exists: cryptocurrency/%s", f)
		}
		spec.legacy = lp
		a.platforms = append(a.platforms, spec)
		log.Printf("%s → %s (%s / %s)", key, lp.id, plat.Name, plat.Symbol)
	}
	return nil
}

func (a *adder) resolveSygnaID(key string) (string, error) {
	if id, ok := a.overrides[key]; ok {
		return id, nil
	}
	if id, ok := knownSygnaIDs[key]; ok {
		return id, nil
	}
	var names map[string]string
	data, err := os.ReadFile(a.path("blockchain_name/mapping.json"))
	if err != nil {
		return "", err
	}
	if err := json.Unmarshal(data, &names); err != nil {
		return "", fmt.Errorf("failed to parse blockchain_name/mapping.json: %v", err)
	}
	want := strings.ReplaceAll(key, "-", "_")
	var matches []string
	for id, n := range names {
		if strings.EqualFold(n, want) {
			matches = append(matches, id)
		}
	}
	sort.Strings(matches)
	if len(matches) == 1 {
		return matches[0], nil
	}
	if len(matches) == 0 {
		return "", fmt.Errorf("cannot find Sygna ID for platform %q in blockchain_name/mapping.json; pass -sygna-id %s=sygna:...", key, key)
	}
	return "", fmt.Errorf("platform %q matches several Sygna IDs %v; pass -sygna-id %s=sygna:...", key, matches, key)
}

func (a *adder) stageMappings(cgV3 map[string]string) error {
	if a.addCGv3 {
		if err := a.appendEntry("coingecko_id/mapping_v3.json", ']', "",
			indentJSON(CoingeckoMapping{a.uuid, a.coingeckoID}, "  ", "  ")); err != nil {
			return err
		}
	}

	var legacies []*legacyPlan
	for _, p := range a.platforms {
		if p.legacy != nil {
			legacies = append(legacies, p.legacy)
		}
	}
	if len(legacies) == 0 {
		return nil
	}

	for _, lp := range legacies {
		data, err := encodeJSON(LegacyCurrency{
			CurrencyID:     lp.id,
			CurrencyName:   a.name,
			CurrencySymbol: a.symbol,
			IsActive:       true,
			AddrExtraInfo:  []string{},
			Platform:       &lp.platform,
		}, "")
		if err != nil {
			return err
		}
		a.stage(lp.filename, data)
		if err := a.appendEntry("coingecko_id/mapping.json", ']', "",
			indentJSON(CoingeckoMapping{lp.id, a.coingeckoID}, "    ", "    ")); err != nil {
			return err
		}
	}

	// new_to_old: extend the UUID's object if present, otherwise add one.
	n2oFile := "id_mapping/new_to_old_id_mapping.json"
	n2o, err := a.read(n2oFile)
	if err != nil {
		return err
	}
	var lines []string
	for _, p := range a.platforms {
		if p.legacy != nil {
			lines = append(lines, fmt.Sprintf("    %s: %s", quote(p.key), quote(p.legacy.id)))
		}
	}
	header := quote(a.uuid) + ": {"
	if start := bytes.Index(n2o, []byte(header)); start >= 0 {
		end := bytes.IndexByte(n2o[start:], '}')
		if end < 0 {
			return fmt.Errorf("malformed %s near %s", n2oFile, a.uuid)
		}
		end += start
		obj, err := appendToContainer(n2o[start:end+1], '}', "  ", strings.Join(lines, ",\n"))
		if err != nil {
			return err
		}
		updated := append(append(append([]byte{}, n2o[:start]...), obj...), n2o[end+1:]...)
		a.stage(n2oFile, updated)
	} else {
		entry := "  " + header + "\n" + strings.Join(lines, ",\n") + "\n  }"
		if err := a.appendEntry(n2oFile, '}', "", entry); err != nil {
			return err
		}
	}

	var o2n []string
	for _, lp := range legacies {
		o2n = append(o2n, fmt.Sprintf("  %s: %s", quote(lp.id), quote(a.uuid)))
	}
	return a.appendEntry("id_mapping/old_to_new_id_mapping.json", '}', "", strings.Join(o2n, ",\n"))
}

// appendEntry appends entry text to the top-level array/object of a file,
// leaving every existing byte untouched apart from the inserted comma.
func (a *adder) appendEntry(rel string, closing byte, closingIndent, entry string) error {
	data, err := a.read(rel)
	if err != nil {
		return err
	}
	out, err := appendToContainer(data, closing, closingIndent, entry)
	if err != nil {
		return fmt.Errorf("%s: %v", rel, err)
	}
	a.stage(rel, out)
	return nil
}

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

// verify re-parses every staged file and checks the cross-references agree.
func (a *adder) verify() error {
	for _, rel := range a.order {
		if !json.Valid(a.files[rel]) {
			return fmt.Errorf("%s is not valid JSON", rel)
		}
		before := map[string]bool{}
		if orig, err := os.ReadFile(a.path(rel)); err == nil {
			for _, d := range duplicateKeys(orig) {
				before[d] = true
			}
		}
		for _, d := range duplicateKeys(a.files[rel]) {
			if !before[d] {
				return fmt.Errorf("%s would contain duplicate key %s", rel, d)
			}
		}
	}
	o2n, err := a.loadStringMap("id_mapping/old_to_new_id_mapping.json")
	if err != nil {
		return err
	}
	var n2o map[string]map[string]string
	data, err := a.read("id_mapping/new_to_old_id_mapping.json")
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &n2o); err != nil {
		return fmt.Errorf("new_to_old_id_mapping.json: %v", err)
	}
	var cg []CoingeckoMapping
	data, err = a.read("coingecko_id/mapping.json")
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &cg); err != nil {
		return fmt.Errorf("coingecko_id/mapping.json: %v", err)
	}
	cgCount := map[string]int{}
	for _, m := range cg {
		cgCount[m.CurrencyID]++
	}
	cgV3, err := a.loadCoingeckoV3()
	if err != nil {
		return err
	}
	if cgV3[a.uuid] != a.coingeckoID {
		return fmt.Errorf("mapping_v3.json: %s → %q, want %q", a.uuid, cgV3[a.uuid], a.coingeckoID)
	}
	for _, p := range a.platforms {
		if p.legacy == nil {
			continue
		}
		id := p.legacy.id
		if o2n[id] != a.uuid {
			return fmt.Errorf("old_to_new: %s → %q, want %s", id, o2n[id], a.uuid)
		}
		if n2o[a.uuid][p.key] != id {
			return fmt.Errorf("new_to_old: %s.%s → %q, want %s", a.uuid, p.key, n2o[a.uuid][p.key], id)
		}
		if cgCount[id] != 1 {
			return fmt.Errorf("mapping.json: %s appears %d times, want 1", id, cgCount[id])
		}
	}
	return nil
}

func (a *adder) printPlan() {
	fmt.Println("Dry run — files that would change:")
	for _, rel := range a.order {
		fmt.Printf("  %s\n", rel)
	}
	fmt.Printf("\nUUID: %s\nCoinGecko: %s\n", a.uuid, a.coingeckoID)
	for _, p := range a.platforms {
		if p.legacy != nil {
			fmt.Printf("  %-22s v3=%s legacy=%s\n", p.key, p.v3Address, p.legacy.id)
		} else {
			fmt.Printf("  %-22s v3=%s (legacy skipped)\n", p.key, p.v3Address)
		}
	}
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
