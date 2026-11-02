package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"sort"
	"strings"
)

// updater changes an existing currency:
//
//	① -set-address KEY:ADDR   new token address on a chain
//	② -set-name / -set-symbol
//	③ -set-coingecko-id
//	④ -remove-platform KEY
//	⑤ -deactivate [-delete-v3] delist the currency
//
// Old legacy IDs are never deleted or renamed: they stay in every mapping file
// with "is_active": false, because Bridge may still resolve them.
type updater struct {
	*repo
	symbol        string
	name          string
	uuid          string
	overrides     map[string]string
	allowSharedCG bool

	setAddress      [][2]string // platform key, new address
	setName         string
	setSymbol       string
	setCoingeckoID  string
	removePlatforms []string
	deactivate      bool
	deleteV3        bool

	v3File      string
	currencies  []Currency
	target      Currency
	v3Changed   bool
	coingeckoID string
	o2n         map[string]string
	n2o         map[string]map[string]string
	idx         *legacyIndex
	legacyIDs   []string          // every legacy ID mapped to the currency
	cgTouched   map[string]bool   // legacy IDs whose mapping.json entry was set
	newLegacy   map[string]bool   // legacy IDs added by ①
	addressSet  map[string]string // platform key → new address (①)
	removed     []string          // platform keys removed (④)
}

// state is what the dry run shows before and after the change.
type state struct {
	name, symbol, coingecko, v3File string
	platforms                       map[string]string
	platformOrder                   []string
	legacy                          map[string]bool // legacy ID → is_active
	legacyOrder                     []string
}

func (u *updater) run(dryRun bool) error {
	if err := u.checkFlags(); err != nil {
		return err
	}
	if err := u.load(); err != nil {
		return err
	}
	before, err := u.state()
	if err != nil {
		return err
	}

	if u.setCoingeckoID != "" {
		if err := u.updateCoingeckoID(); err != nil {
			return err
		}
	}
	if u.setName != "" || u.setSymbol != "" {
		if err := u.updateNameSymbol(); err != nil {
			return err
		}
	}
	for _, s := range u.setAddress {
		if err := u.updateAddress(s[0], s[1]); err != nil {
			return err
		}
	}
	for _, key := range u.removePlatforms {
		if err := u.removePlatform(key); err != nil {
			return err
		}
	}
	if u.deactivate {
		if err := u.deactivateAll(); err != nil {
			return err
		}
	}
	if err := u.stageV3(); err != nil {
		return err
	}

	if err := u.verify(); err != nil {
		return fmt.Errorf("self-check failed, nothing written: %v", err)
	}
	for _, rel := range u.order {
		if err := u.checkInsideRoot(rel); err != nil {
			return err
		}
	}
	if dryRun {
		after, err := u.state()
		if err != nil {
			return err
		}
		u.printPlan(before, after)
		return nil
	}
	return u.writeAll()
}

func (u *updater) checkFlags() error {
	changes := len(u.setAddress) + len(u.removePlatforms)
	for _, s := range []string{u.setName, u.setSymbol, u.setCoingeckoID} {
		if s != "" {
			changes++
		}
	}
	switch {
	case u.deleteV3 && !u.deactivate:
		return fmt.Errorf("-delete-v3 only works together with -deactivate")
	case u.deactivate && changes > 0:
		return fmt.Errorf("-deactivate cannot be combined with other changes")
	case !u.deactivate && changes == 0:
		return fmt.Errorf("nothing to update: pass -set-address, -set-name, -set-symbol, -set-coingecko-id, -remove-platform or -deactivate")
	}
	if u.setSymbol != "" && (!symbolRe.MatchString(u.setSymbol) || strings.Contains(u.setSymbol, "..")) {
		return fmt.Errorf("invalid -set-symbol %q: only A-Z, 0-9, '$', '.', '_' and '-' are allowed", u.setSymbol)
	}
	seen := map[string]string{}
	for _, s := range u.setAddress {
		if prev, dup := seen[s[0]]; dup {
			return fmt.Errorf("platform %q is given to both %s and -set-address", s[0], prev)
		}
		seen[s[0]] = "-set-address"
	}
	for _, key := range u.removePlatforms {
		if prev, dup := seen[key]; dup {
			return fmt.Errorf("platform %q is given to both %s and -remove-platform", key, prev)
		}
		seen[key] = "-remove-platform"
	}
	return nil
}

// load finds the currency and indexes every file that refers to it.
func (u *updater) load() error {
	u.v3File = fmt.Sprintf("cryptocurrency_v3/%s.json", u.symbol)
	currencies, err := u.loadV3File(u.v3File)
	if err != nil {
		return err
	}
	if currencies == nil {
		return fmt.Errorf("%s not found: no currency with symbol %s", u.v3File, u.symbol)
	}
	i, err := matchCurrency(currencies, u.name, u.uuid, u.v3File)
	if err != nil {
		return err
	}
	if i < 0 {
		return fmt.Errorf("%s has no currency named %q", u.v3File, u.name)
	}
	u.currencies, u.target, u.uuid = currencies, currencies[i], currencies[i].ID
	log.Printf("Updating %q (%s, UUID %s)", u.name, u.symbol, u.uuid)

	cgV3, err := u.loadCoingeckoV3()
	if err != nil {
		return err
	}
	u.coingeckoID = cgV3[u.uuid]
	if u.o2n, err = u.loadStringMap("id_mapping/old_to_new_id_mapping.json"); err != nil {
		return err
	}
	if u.n2o, err = u.loadNewToOld(); err != nil {
		return err
	}
	if u.idx, err = u.loadLegacyIndex(); err != nil {
		return err
	}
	for id, owner := range u.o2n {
		if owner == u.uuid {
			if _, ok := u.idx.fileForID(id); !ok {
				return fmt.Errorf("legacy ID %s has no file in cryptocurrency/", id)
			}
			u.legacyIDs = append(u.legacyIDs, id)
		}
	}
	sort.Strings(u.legacyIDs)
	u.cgTouched, u.newLegacy, u.addressSet = map[string]bool{}, map[string]bool{}, map[string]string{}
	return nil
}

func (u *updater) loadV3File(rel string) ([]Currency, error) {
	data, err := u.read(rel)
	if os.IsNotExist(err) {
		return nil, nil
	} else if err != nil {
		return nil, err
	}
	var list []Currency
	if err := json.Unmarshal(data, &list); err != nil {
		return nil, fmt.Errorf("failed to parse %s: %v", rel, err)
	}
	return list, nil
}

func (u *updater) loadNewToOld() (map[string]map[string]string, error) {
	data, err := u.read("id_mapping/new_to_old_id_mapping.json")
	if err != nil {
		return nil, err
	}
	var m map[string]map[string]string
	if err := json.Unmarshal(data, &m); err != nil {
		return nil, fmt.Errorf("failed to parse id_mapping/new_to_old_id_mapping.json: %v", err)
	}
	return m, nil
}

func (u *updater) legacyFile(id string) string {
	f, _ := u.idx.fileForID(id)
	if f == "" {
		f = "sygna-" + strings.TrimPrefix(id, "sygna:") + ".json"
	}
	return "cryptocurrency/" + f
}

// editLegacy applies setField edits to a legacy file, keeping its formatting.
func (u *updater) editLegacy(id string, fields ...any) error {
	rel := u.legacyFile(id)
	data, err := u.read(rel)
	if err != nil {
		return err
	}
	for i := 0; i < len(fields); i += 2 {
		if data, err = setField(data, fields[i].(string), fields[i+1]); err != nil {
			return fmt.Errorf("%s: %v", rel, err)
		}
	}
	u.stage(rel, data)
	return nil
}

func (u *updater) legacyActive(id string) (bool, error) {
	data, err := u.read(u.legacyFile(id))
	if err != nil {
		return false, err
	}
	var active bool
	if err := fieldValue(data, "is_active", &active); err != nil {
		return false, fmt.Errorf("%s: %v", u.legacyFile(id), err)
	}
	return active, nil
}

func (u *updater) setInactive(id string) error {
	active, err := u.legacyActive(id)
	if err != nil || !active {
		return err
	}
	log.Printf("%s → inactive", id)
	return u.editLegacy(id, "is_active", false)
}

// n2oEntry returns the legacy ID new_to_old maps the currency's key to.
func (u *updater) n2oEntry(key string) (string, string, bool) {
	for k, id := range u.n2o[u.uuid] {
		if strings.EqualFold(k, key) {
			return k, id, true
		}
	}
	return "", "", false
}

// editN2O applies fn to the currency's object in new_to_old_id_mapping.json.
func (u *updater) editN2O(fn func(obj []byte) ([]byte, error)) error {
	const rel = "id_mapping/new_to_old_id_mapping.json"
	data, err := u.read(rel)
	if err != nil {
		return err
	}
	start, end, ok := objectSpan(data, u.uuid)
	if !ok {
		return fmt.Errorf("%s has no object for %s", rel, u.uuid)
	}
	obj, err := fn(data[start:end])
	if err != nil {
		return fmt.Errorf("%s: %v", rel, err)
	}
	u.stage(rel, splice(data, start, end, obj))
	u.n2o, err = u.loadNewToOld()
	return err
}

// ③
func (u *updater) updateCoingeckoID() error {
	id := u.setCoingeckoID
	if id == u.coingeckoID {
		return fmt.Errorf("%q already uses CoinGecko ID %q", u.name, id)
	}
	cgV3, err := u.loadCoingeckoV3()
	if err != nil {
		return err
	}
	if !u.allowSharedCG {
		for other, cg := range cgV3 {
			if cg == id && other != u.uuid {
				return fmt.Errorf("CoinGecko ID %q is already mapped to currency %s; if this is really the same coin, pass -allow-shared-coingecko-id", id, other)
			}
		}
	}

	const v3Map = "coingecko_id/mapping_v3.json"
	data, err := u.read(v3Map)
	if err != nil {
		return err
	}
	out, _, found, err := setCoingeckoID(data, u.uuid, id)
	if err != nil {
		return fmt.Errorf("%s: %v", v3Map, err)
	}
	if found {
		u.stage(v3Map, out)
	} else if err := u.appendEntry(v3Map, ']', "", indentJSON(CoingeckoMapping{u.uuid, id}, "  ", "  ")); err != nil {
		return err
	}

	const legacyMap = "coingecko_id/mapping.json"
	if data, err = u.read(legacyMap); err != nil {
		return err
	}
	for _, lid := range u.legacyIDs {
		out, _, found, err := setCoingeckoID(data, lid, id)
		if err != nil {
			return fmt.Errorf("%s: %v", legacyMap, err)
		}
		if found {
			data = out
			u.cgTouched[lid] = true
		}
	}
	u.stage(legacyMap, data)
	log.Printf("CoinGecko ID %q → %q", u.coingeckoID, id)
	u.coingeckoID = id
	return nil
}

// ②
func (u *updater) updateNameSymbol() error {
	name, symbol := u.target.Name, u.target.Symbol
	if u.setName != "" {
		name = u.setName
	}
	if u.setSymbol != "" {
		symbol = u.setSymbol
	}
	if name == u.target.Name && symbol == u.target.Symbol {
		return fmt.Errorf("name and symbol are already %q / %s", name, symbol)
	}

	dest := u.currencies
	if symbol != u.target.Symbol {
		var err error
		if dest, err = u.loadV3File(fmt.Sprintf("cryptocurrency_v3/%s.json", symbol)); err != nil {
			return err
		}
	}
	for _, c := range dest {
		if c.ID != u.uuid && strings.EqualFold(c.Name, name) {
			return fmt.Errorf("cryptocurrency_v3/%s.json already has a currency named %q (%s)", symbol, c.Name, c.ID)
		}
	}

	for _, id := range u.legacyIDs {
		var fields []any
		if name != u.target.Name {
			fields = append(fields, "currency_name", name)
		}
		if symbol != u.target.Symbol {
			fields = append(fields, "currency_symbol", symbol)
		}
		if err := u.editLegacy(id, fields...); err != nil {
			return err
		}
	}
	log.Printf("%q (%s) → %q (%s)", u.target.Name, u.target.Symbol, name, symbol)
	u.target.Name, u.target.Symbol = name, symbol
	u.v3Changed = true
	return nil
}

// ①
func (u *updater) updateAddress(key, addr string) error {
	k, ok := u.target.Platforms.Key(key)
	if !ok {
		return fmt.Errorf("%q has no %s platform", u.target.Name, key)
	}
	old := u.target.Platforms.m[k].TokenAddress
	switch {
	case strings.EqualFold(old, "native"):
		return fmt.Errorf("%s is native on %q; native platforms must be changed manually", k, u.target.Name)
	case strings.EqualFold(addr, "native"):
		return fmt.Errorf("cannot change %s to native here; native platforms must be changed manually", k)
	case strings.EqualFold(old, addr):
		return fmt.Errorf("%s already uses address %s", k, old)
	}
	if err := validateAddress(k, addr); err != nil {
		return err
	}
	if evmAddressRe.MatchString(addr) {
		addr = strings.ToLower(addr)
	}
	used, err := u.loadV3Addresses()
	if err != nil {
		return err
	}
	if owner, dup := used[strings.ToLower(k)+"|"+strings.ToLower(addr)]; dup {
		return fmt.Errorf("address %s on %s is already used by %s", addr, k, owner)
	}
	sygnaID, err := u.resolveSygnaID(u.overrides, strings.ToLower(k))
	if err != nil {
		return err
	}
	lp, err := newLegacyPlan(u.idx, k, sygnaID, addr)
	if err != nil {
		return err
	}
	for existing := range u.o2n {
		if strings.EqualFold(existing, lp.id) {
			return fmt.Errorf("legacy ID %s already mapped in old_to_new_id_mapping.json", existing)
		}
	}
	if f, dup := u.idx.fileForID(lp.id); dup {
		return fmt.Errorf("legacy file already exists: cryptocurrency/%s", f)
	}

	// The new legacy file copies name, symbol and extra info from the old one
	// (after any ② edits), so it matches the rest of the currency.
	cur := LegacyCurrency{CurrencyName: u.target.Name, CurrencySymbol: u.target.Symbol, AddrExtraInfo: []string{}}
	n2oKey, oldID, hasOld := u.n2oEntry(k)
	if hasOld {
		data, err := u.read(u.legacyFile(oldID))
		if err != nil {
			return err
		}
		var prev LegacyCurrency
		if err := json.Unmarshal(data, &prev); err != nil {
			return fmt.Errorf("%s: %v", u.legacyFile(oldID), err)
		}
		cur.CurrencyName, cur.CurrencySymbol = prev.CurrencyName, prev.CurrencySymbol
		if prev.AddrExtraInfo != nil {
			cur.AddrExtraInfo = prev.AddrExtraInfo
		}
	}
	cur.CurrencyID, cur.IsActive, cur.Platform = lp.id, true, &lp.platform
	data, err := encodeJSON(cur, "")
	if err != nil {
		return err
	}
	u.stage(lp.filename, data)

	if hasOld {
		if err := u.setInactive(oldID); err != nil {
			return err
		}
		err = u.editN2O(func(obj []byte) ([]byte, error) { return setField(obj, n2oKey, lp.id) })
	} else {
		err = u.editN2O(func(obj []byte) ([]byte, error) {
			return appendToContainer(obj, '}', "  ", fmt.Sprintf("    %s: %s", quote(k), quote(lp.id)))
		})
	}
	if err != nil {
		return err
	}
	if u.coingeckoID != "" {
		if err := u.appendEntry("coingecko_id/mapping.json", ']', "",
			indentJSON(CoingeckoMapping{lp.id, u.coingeckoID}, "    ", "    ")); err != nil {
			return err
		}
		u.cgTouched[lp.id] = true
	}
	if err := u.appendEntry("id_mapping/old_to_new_id_mapping.json", '}', "",
		fmt.Sprintf("  %s: %s", quote(lp.id), quote(u.uuid))); err != nil {
		return err
	}
	u.o2n[lp.id] = u.uuid
	u.legacyIDs = append(u.legacyIDs, lp.id)
	u.newLegacy[lp.id] = true
	u.idx.files[strings.ToLower(lp.id)] = strings.TrimPrefix(lp.filename, "cryptocurrency/")

	u.target.Platforms.Set(k, PlatformInfo{TokenAddress: strings.ToLower(addr)})
	u.addressSet[k] = lp.id
	u.v3Changed = true
	log.Printf("%s: %s → %s (%s)", k, old, addr, lp.id)
	return nil
}

// ④
func (u *updater) removePlatform(key string) error {
	k, ok := u.target.Platforms.Key(key)
	if !ok {
		return fmt.Errorf("%q has no %s platform", u.target.Name, key)
	}
	if len(u.target.Platforms.keys) == 1 {
		return fmt.Errorf("cannot remove %s, the last platform of %q; use -deactivate to delist it", k, u.target.Name)
	}
	if n2oKey, id, ok := u.n2oEntry(k); ok {
		if err := u.setInactive(id); err != nil {
			return err
		}
		if err := u.editN2O(func(obj []byte) ([]byte, error) { return removeEntry(obj, n2oKey) }); err != nil {
			return err
		}
	} else {
		log.Printf("⚠️ %s has no legacy ID in new_to_old_id_mapping.json; removing it from v3 only", k)
	}
	u.target.Platforms.Remove(k)
	u.removed = append(u.removed, k)
	u.v3Changed = true
	log.Printf("removed %s", k)
	return nil
}

// ⑤
func (u *updater) deactivateAll() error {
	active := 0
	for _, id := range u.legacyIDs {
		ok, err := u.legacyActive(id)
		if err != nil {
			return err
		}
		if ok {
			active++
		}
	}
	if active == 0 && !u.deleteV3 {
		return fmt.Errorf("%q has no active legacy files; pass -delete-v3 to also remove it from v3", u.name)
	}
	for _, id := range u.legacyIDs {
		if err := u.setInactive(id); err != nil {
			return err
		}
	}
	if u.deleteV3 {
		u.v3Changed = true
		log.Printf("removing %q from %s", u.name, u.v3File)
	}
	return nil
}

// stageV3 writes the currency back: in place, moved to another symbol file,
// or removed with -delete-v3. A symbol file left empty is deleted.
func (u *updater) stageV3() error {
	if !u.v3Changed {
		return nil
	}
	var rest []Currency
	for _, c := range u.currencies {
		if c.ID != u.uuid {
			rest = append(rest, c)
		}
	}
	newFile := fmt.Sprintf("cryptocurrency_v3/%s.json", u.target.Symbol)
	switch {
	case u.deleteV3:
	case newFile == u.v3File:
		rest = nil
		for _, c := range u.currencies {
			if c.ID == u.uuid {
				c = u.target
			}
			rest = append(rest, c)
		}
	default:
		dest, err := u.loadV3File(newFile)
		if err != nil {
			return err
		}
		data, err := encodeJSON(append(dest, u.target), "  ")
		if err != nil {
			return err
		}
		u.stage(newFile, data)
	}
	if len(rest) == 0 {
		u.stageDelete(u.v3File)
		return nil
	}
	data, err := encodeJSON(rest, "  ")
	if err != nil {
		return err
	}
	u.stage(u.v3File, data)
	return nil
}

// verify re-reads the staged files and checks they agree with each other.
func (u *updater) verify() error {
	if err := u.verifyStaged(); err != nil {
		return err
	}
	o2n, err := u.loadStringMap("id_mapping/old_to_new_id_mapping.json")
	if err != nil {
		return err
	}
	n2o, err := u.loadNewToOld()
	if err != nil {
		return err
	}
	cgV3, err := u.loadCoingeckoV3()
	if err != nil {
		return err
	}
	var cg []CoingeckoMapping
	data, err := u.read("coingecko_id/mapping.json")
	if err != nil {
		return err
	}
	if err := json.Unmarshal(data, &cg); err != nil {
		return fmt.Errorf("coingecko_id/mapping.json: %v", err)
	}
	cgByID := map[string][]string{}
	for _, m := range cg {
		cgByID[m.CurrencyID] = append(cgByID[m.CurrencyID], m.CoingeckoID)
	}

	if cgV3[u.uuid] != u.coingeckoID {
		return fmt.Errorf("mapping_v3.json: %s → %q, want %q", u.uuid, cgV3[u.uuid], u.coingeckoID)
	}
	for id := range u.cgTouched {
		if got := cgByID[id]; len(got) != 1 || got[0] != u.coingeckoID {
			return fmt.Errorf("mapping.json: %s → %v, want [%s]", id, got, u.coingeckoID)
		}
	}
	for _, id := range u.legacyIDs {
		if o2n[id] != u.uuid {
			return fmt.Errorf("old_to_new: %s → %q, want %s", id, o2n[id], u.uuid)
		}
		data, err := u.read(u.legacyFile(id))
		if err != nil {
			return fmt.Errorf("legacy file for %s: %v", id, err)
		}
		var lc LegacyCurrency
		if err := json.Unmarshal(data, &lc); err != nil {
			return fmt.Errorf("%s: %v", u.legacyFile(id), err)
		}
		if lc.CurrencyID != id {
			return fmt.Errorf("%s: currency_id %q, want %q", u.legacyFile(id), lc.CurrencyID, id)
		}
		if u.setName != "" && lc.CurrencyName != u.target.Name {
			return fmt.Errorf("%s: currency_name %q, want %q", u.legacyFile(id), lc.CurrencyName, u.target.Name)
		}
		if u.setSymbol != "" && lc.CurrencySymbol != u.target.Symbol {
			return fmt.Errorf("%s: currency_symbol %q, want %q", u.legacyFile(id), lc.CurrencySymbol, u.target.Symbol)
		}
		if u.deactivate && lc.IsActive {
			return fmt.Errorf("%s is still active", u.legacyFile(id))
		}
	}

	final, err := u.loadV3File(fmt.Sprintf("cryptocurrency_v3/%s.json", u.target.Symbol))
	if err != nil {
		return err
	}
	count := 0
	for _, c := range final {
		if c.ID == u.uuid {
			count++
		}
	}
	if u.deleteV3 {
		if count != 0 {
			return fmt.Errorf("%s still in %s", u.uuid, u.v3File)
		}
		return nil
	}
	if count != 1 {
		return fmt.Errorf("cryptocurrency_v3/%s.json has %s %d times, want once", u.target.Symbol, u.uuid, count)
	}
	for _, k := range u.removed {
		if _, ok := u.target.Platforms.Key(k); ok {
			return fmt.Errorf("%s is still in v3", k)
		}
		for nk := range n2o[u.uuid] {
			if strings.EqualFold(nk, k) {
				return fmt.Errorf("new_to_old: %s still maps %s", u.uuid, k)
			}
		}
	}
	for k, id := range u.addressSet {
		if n2o[u.uuid][k] != id {
			return fmt.Errorf("new_to_old: %s.%s → %q, want %s", u.uuid, k, n2o[u.uuid][k], id)
		}
		data, err := u.read(u.legacyFile(id))
		if err != nil {
			return fmt.Errorf("legacy file for %s: %v", id, err)
		}
		var lc LegacyCurrency
		if err := json.Unmarshal(data, &lc); err != nil {
			return err
		}
		addr := u.target.Platforms.m[k].TokenAddress
		if !lc.IsActive || lc.Platform == nil || !strings.EqualFold(lc.Platform.TokenAddress, addr) {
			return fmt.Errorf("%s does not match v3 address %s on %s", u.legacyFile(id), addr, k)
		}
	}
	return nil
}

func (u *updater) state() (state, error) {
	s := state{name: u.target.Name, symbol: u.target.Symbol, platforms: map[string]string{}, legacy: map[string]bool{}}
	s.v3File = fmt.Sprintf("cryptocurrency_v3/%s.json", u.target.Symbol)
	if u.deleteV3 && u.v3Changed {
		s.v3File = "(removed from v3)"
	}
	cgV3, err := u.loadCoingeckoV3()
	if err != nil {
		return s, err
	}
	s.coingecko = cgV3[u.uuid]
	for _, k := range u.target.Platforms.keys {
		s.platforms[k] = u.target.Platforms.m[k].TokenAddress
		s.platformOrder = append(s.platformOrder, k)
	}
	for _, id := range u.legacyIDs {
		active, err := u.legacyActive(id)
		if err != nil {
			return s, err
		}
		s.legacy[id] = active
		s.legacyOrder = append(s.legacyOrder, id)
	}
	return s, nil
}

func (u *updater) printPlan(before, after state) {
	fmt.Println("Dry run — files that would change:")
	for _, rel := range u.order {
		fmt.Printf("  %-6s %s\n", u.changeKind(rel), rel)
	}
	fmt.Printf("\nCurrency %s\n", u.uuid)
	row := func(label, b, a string) {
		if b == a {
			fmt.Printf("  %-26s %s\n", label, b)
		} else {
			fmt.Printf("  %-26s %s → %s\n", label, b, a)
		}
	}
	row("v3 file", u.v3File, after.v3File)
	row("name", before.name, after.name)
	row("symbol", before.symbol, after.symbol)
	row("coingecko", before.coingecko, after.coingecko)
	for _, k := range before.platformOrder {
		a, ok := after.platforms[k]
		if !ok {
			a = "(removed)"
		}
		row("platform "+k, before.platforms[k], a)
	}
	activeWord := func(b bool) string {
		if b {
			return "active"
		}
		return "inactive"
	}
	for _, id := range after.legacyOrder {
		b := "(new)"
		if v, ok := before.legacy[id]; ok {
			b = activeWord(v)
		}
		row("legacy "+id, b, activeWord(after.legacy[id]))
	}
}
