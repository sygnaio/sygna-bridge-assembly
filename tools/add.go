package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"regexp"
	"sort"
	"strings"

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
	"kaia":                true,
	"ronin":               true,
	"harmonyone":          true,
	"celo":                true,
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

type adder struct {
	*repo
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
}

// matchCurrency returns the index of the currency named name in v3File's
// currencies, or -1 if there is none. Some symbol files hold several
// currencies with the exact same name (e.g. SPA.json has three "Sperax"), so
// it never silently picks the first.
func matchCurrency(currencies []Currency, name, uuid, v3File string) (int, error) {
	var exact []int
	caseOnly := ""
	for i := range currencies {
		switch {
		case currencies[i].Name == name:
			exact = append(exact, i)
		case strings.EqualFold(currencies[i].Name, name):
			caseOnly = currencies[i].Name
		}
	}
	switch {
	case len(exact) == 1:
		if uuid != "" && uuid != currencies[exact[0]].ID {
			return -1, fmt.Errorf("-uuid %s does not match existing %q (%s)", uuid, name, currencies[exact[0]].ID)
		}
		return exact[0], nil
	case len(exact) > 1:
		var ids []string
		for _, i := range exact {
			ids = append(ids, currencies[i].ID)
			if currencies[i].ID == uuid {
				return i, nil
			}
		}
		return -1, fmt.Errorf("%s has %d currencies named %q (%s); pass -uuid to choose one", v3File, len(exact), name, strings.Join(ids, ", "))
	case caseOnly != "":
		return -1, fmt.Errorf("%s already has currency %q (differs only in case from %q); use the exact existing name", v3File, caseOnly, name)
	}
	return -1, nil
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
	i, err := matchCurrency(currencies, a.name, a.uuid, v3File)
	if err != nil {
		return err
	}
	if i >= 0 {
		target = &currencies[i]
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

		sygnaID, err := a.resolveSygnaID(a.overrides, key)
		if err != nil {
			return err
		}
		lp, err := newLegacyPlan(legacyIndex, key, sygnaID, addr)
		if err != nil {
			return err
		}

		if orig, dup := existingIDs[strings.ToLower(lp.id)]; dup {
			return fmt.Errorf("legacy ID %s already mapped in old_to_new_id_mapping.json", orig)
		}
		if f, dup := legacyIndex.fileForID(lp.id); dup {
			return fmt.Errorf("legacy file already exists: cryptocurrency/%s", f)
		}
		spec.legacy = lp
		a.platforms = append(a.platforms, spec)
		log.Printf("%s → %s (%s / %s)", key, lp.id, lp.platform.Name, lp.platform.Symbol)
	}
	return nil
}

// newLegacyPlan describes the legacy file for a token address on sygnaID,
// copying the platform name/symbol from existing tokens on that chain.
func newLegacyPlan(idx *legacyIndex, key, sygnaID, addr string) (*legacyPlan, error) {
	plat, ok := idx.platformFor(sygnaID)
	if !ok {
		return nil, fmt.Errorf("no existing legacy token on %s (%s) to copy platform name/symbol from; add this platform manually", key, sygnaID)
	}
	lp := &legacyPlan{
		sygnaID:  sygnaID,
		address:  addr,
		id:       sygnaID + "." + addr,
		platform: LegacyPlatform{Name: plat.Name, Symbol: plat.Symbol, TokenAddress: addr, ID: sygnaID},
	}
	lp.filename = "cryptocurrency/sygna-" + strings.TrimPrefix(lp.id, "sygna:") + ".json"
	return lp, nil
}

func (r *repo) resolveSygnaID(overrides map[string]string, key string) (string, error) {
	if id, ok := overrides[key]; ok {
		return id, nil
	}
	if id, ok := knownSygnaIDs[key]; ok {
		return id, nil
	}
	var names map[string]string
	data, err := os.ReadFile(r.path("blockchain_name/mapping.json"))
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
func (r *repo) appendEntry(rel string, closing byte, closingIndent, entry string) error {
	data, err := r.read(rel)
	if err != nil {
		return err
	}
	out, err := appendToContainer(data, closing, closingIndent, entry)
	if err != nil {
		return fmt.Errorf("%s: %v", rel, err)
	}
	r.stage(rel, out)
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
