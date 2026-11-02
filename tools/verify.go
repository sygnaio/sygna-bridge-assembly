package main

import (
	"encoding/json"
	"fmt"
	"os"
)

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
