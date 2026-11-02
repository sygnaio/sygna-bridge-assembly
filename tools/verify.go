package main

import (
	"encoding/json"
	"fmt"
	"os"
)

// verifyStaged checks that every staged file is valid JSON and adds no
// duplicate keys that the original file did not already have.
func (r *repo) verifyStaged() error {
	for _, rel := range r.order {
		if r.deleted[rel] {
			continue
		}
		if !json.Valid(r.files[rel]) {
			return fmt.Errorf("%s is not valid JSON", rel)
		}
		before := map[string]bool{}
		if orig, err := os.ReadFile(r.path(rel)); err == nil {
			for _, d := range duplicateKeys(orig) {
				before[d] = true
			}
		}
		for _, d := range duplicateKeys(r.files[rel]) {
			if !before[d] {
				return fmt.Errorf("%s would contain duplicate key %s", rel, d)
			}
		}
	}
	return nil
}

// verify re-parses every staged file and checks the cross-references agree.
func (a *adder) verify() error {
	if err := a.verifyStaged(); err != nil {
		return err
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
