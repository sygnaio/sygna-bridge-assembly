package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const (
	zilETHFile    = "cryptocurrency/sygna-0x8000003c.0x05f4a42e251f2d52b8ed15e9fedaacfcef1fad27.json"
	zilBSCFile    = "cryptocurrency/sygna-0x8000232e.0xb86abcb37c3a4b64f74f59301aff131a1becc787.json"
	zilNativeFile = "cryptocurrency/sygna:0x80000139.json"
	mvETHFile     = "cryptocurrency/sygna-0x8000003c.0xae788f80f2756a86aa2f410c651f2af83639b95b.json"
	mvPolFile     = "cryptocurrency/sygna-0x800003c6.0xa3c322ad15218fbfaed26ba7f616249f7705d945.json"
	n2oFile       = "id_mapping/new_to_old_id_mapping.json"
	o2nFile       = "id_mapping/old_to_new_id_mapping.json"
	cgFile        = "coingecko_id/mapping.json"
	cgV3File      = "coingecko_id/mapping_v3.json"
)

// newUpdater builds an updater the way main does after flag parsing.
func newUpdater(root, symbol, name string) *updater {
	return &updater{repo: newRepo(root), symbol: symbol, name: name, overrides: map[string]string{}}
}

// runUpdate runs u and returns the snapshots before and after.
func runUpdate(t *testing.T, root string, u *updater) (before, after map[string]string) {
	t.Helper()
	before = snapshot(t, root)
	if err := u.run(false); err != nil {
		t.Fatal(err)
	}
	return before, snapshot(t, root)
}

func assertChanged(t *testing.T, before, after map[string]string, want ...string) {
	t.Helper()
	got := changed(before, after)
	if strings.Join(got, "\n") != strings.Join(sortedCopy(want), "\n") {
		t.Fatalf("changed files:\n  %s\nwant:\n  %s", strings.Join(got, "\n  "), strings.Join(sortedCopy(want), "\n  "))
	}
}

func sortedCopy(s []string) []string {
	out := append([]string(nil), s...)
	for i := range out {
		for j := i + 1; j < len(out); j++ {
			if out[j] < out[i] {
				out[i], out[j] = out[j], out[i]
			}
		}
	}
	return out
}

// assertReplaced checks that after equals before with old replaced by new
// exactly n times, and nothing else changed.
func assertReplaced(t *testing.T, file, before, after, old, new string, n int) {
	t.Helper()
	if c := strings.Count(before, old); c != n {
		t.Fatalf("%s: %q appears %d times before, want %d", file, old, c, n)
	}
	if want := strings.ReplaceAll(before, old, new); after != want {
		t.Errorf("%s:\n got %s\nwant %s", file, tail(after), tail(want))
	}
}

func TestUpdateCoingeckoID(t *testing.T) {
	root := copyRepo(t)
	u := newUpdater(root, "ZIL", "Zilliqa")
	u.setCoingeckoID = "zil-new"
	before, after := runUpdate(t, root, u)

	assertChanged(t, before, after, cgV3File, cgFile)
	assertReplaced(t, cgV3File, before[cgV3File], after[cgV3File], `"coingecko_id": "zilliqa"`, `"coingecko_id": "zil-new"`, 1)
	// all three ZIL legacy IDs (native, Ethereum, BEP-20)
	assertReplaced(t, cgFile, before[cgFile], after[cgFile], `"coingecko_id": "zilliqa"`, `"coingecko_id": "zil-new"`, 3)
}

func TestUpdateNameAndSymbolMovesV3File(t *testing.T) {
	root := copyRepo(t)
	u := newUpdater(root, "MV", "GensoKishi Metaverse")
	u.setName, u.setSymbol = "Genso", "GENSO"
	before, after := runUpdate(t, root, u)

	assertChanged(t, before, after, "cryptocurrency_v3/MV.json", "cryptocurrency_v3/GENSO.json", mvETHFile, mvPolFile)
	if _, ok := after["cryptocurrency_v3/MV.json"]; ok {
		t.Error("MV.json should be deleted once its only currency moved")
	}
	want := strings.NewReplacer(`"symbol": "MV"`, `"symbol": "GENSO"`, `"name": "GensoKishi Metaverse"`, `"name": "Genso"`).
		Replace(before["cryptocurrency_v3/MV.json"])
	if got := after["cryptocurrency_v3/GENSO.json"]; got != want {
		t.Errorf("GENSO.json:\n%s\nwant:\n%s", got, want)
	}
	// legacy files keep their own field order (the Polygon one has "id" first)
	for _, f := range []string{mvETHFile, mvPolFile} {
		want := strings.NewReplacer(`"currency_name":"GensoKishi Metaverse"`, `"currency_name":"Genso"`, `"currency_symbol":"MV"`, `"currency_symbol":"GENSO"`).
			Replace(before[f])
		if after[f] != want {
			t.Errorf("%s:\n%s\nwant:\n%s", f, after[f], want)
		}
	}
}

func TestUpdateNameKeepsOtherCurrenciesInFile(t *testing.T) {
	root := copyRepo(t)
	u := newUpdater(root, "CHI", "Xaya")
	u.setName = "Xaya Coin"
	before, after := runUpdate(t, root, u)

	assertChanged(t, before, after, "cryptocurrency_v3/CHI.json", "cryptocurrency/sygna:0x800001fd.json")
	assertReplaced(t, "CHI.json", before["cryptocurrency_v3/CHI.json"], after["cryptocurrency_v3/CHI.json"], `"name": "Xaya"`, `"name": "Xaya Coin"`, 1)
	f := "cryptocurrency/sygna:0x800001fd.json"
	assertReplaced(t, f, before[f], after[f], `"currency_name":"Xaya"`, `"currency_name":"Xaya Coin"`, 1)
}

func TestUpdateAddress(t *testing.T) {
	root := copyRepo(t)
	newAddr := "0x05F4A42E251F2D52B8ED15E9FEDAACFCEF1FAD99"
	lower := strings.ToLower(newAddr)
	newID := "sygna:0x8000003c." + lower
	newFile := "cryptocurrency/sygna-0x8000003c." + lower + ".json"
	oldID := "sygna:0x8000003c.0x05f4a42e251f2d52b8ed15e9fedaacfcef1fad27"

	u := newUpdater(root, "ZIL", "Zilliqa")
	u.setAddress = [][2]string{{"ethereum", newAddr}}
	before, after := runUpdate(t, root, u)

	assertChanged(t, before, after, "cryptocurrency_v3/ZIL.json", newFile, zilETHFile, cgFile, n2oFile, o2nFile)
	assertReplaced(t, "ZIL.json", before["cryptocurrency_v3/ZIL.json"], after["cryptocurrency_v3/ZIL.json"],
		"0x05f4a42e251f2d52b8ed15e9fedaacfcef1fad27", lower, 1)
	wantNew := `{"currency_id":"` + newID + `","currency_name":"Zilliqa","currency_symbol":"ZIL","is_active":true,"addr_extra_info":[],"platform":{"name":"Ethereum","symbol":"ETH","token_address":"` + lower + `","id":"sygna:0x8000003c"}}`
	if after[newFile] != wantNew {
		t.Errorf("%s:\n%s\nwant:\n%s", newFile, after[newFile], wantNew)
	}
	assertReplaced(t, zilETHFile, before[zilETHFile], after[zilETHFile], `"is_active":true`, `"is_active":false`, 1)
	assertReplaced(t, n2oFile, before[n2oFile], after[n2oFile], `"ethereum": "`+oldID+`"`, `"ethereum": "`+newID+`"`, 1)
	assertAppended(t, cgFile, before[cgFile], after[cgFile], "    {\n        \"currency_id\": \""+newID+"\",\n        \"coingecko_id\": \"zilliqa\"\n    }", ']')
	assertAppended(t, o2nFile, before[o2nFile], after[o2nFile], "  \""+newID+"\": \""+zilUUID+"\"", '}')
}

func TestUpdateRemovePlatform(t *testing.T) {
	root := copyRepo(t)
	u := newUpdater(root, "ZIL", "Zilliqa")
	u.removePlatforms = []string{"binance-smart-chain"}
	before, after := runUpdate(t, root, u)

	assertChanged(t, before, after, "cryptocurrency_v3/ZIL.json", zilBSCFile, n2oFile)
	v3 := "cryptocurrency_v3/ZIL.json"
	assertReplaced(t, v3, before[v3], after[v3],
		"      },\n      \"binance-smart-chain\": {\n        \"token_address\": \"0xb86abcb37c3a4b64f74f59301aff131a1becc787\"\n      }\n", "      }\n", 1)
	assertReplaced(t, zilBSCFile, before[zilBSCFile], after[zilBSCFile], `"is_active":true`, `"is_active":false`, 1)
	// BEP-20 was the last line of the ZIL object, so the comma moves up
	assertReplaced(t, n2oFile, before[n2oFile], after[n2oFile],
		"\"sygna:0x8000003c.0x05f4a42e251f2d52b8ed15e9fedaacfcef1fad27\",\n    \"binance-smart-chain\": \"sygna:0x8000232e.0xb86abcb37c3a4b64f74f59301aff131a1becc787\"\n",
		"\"sygna:0x8000003c.0x05f4a42e251f2d52b8ed15e9fedaacfcef1fad27\"\n", 1)
}

func TestUpdateDeactivate(t *testing.T) {
	root := copyRepo(t)
	u := newUpdater(root, "ZIL", "Zilliqa")
	u.deactivate = true
	before, after := runUpdate(t, root, u)

	assertChanged(t, before, after, zilNativeFile, zilETHFile, zilBSCFile)
	for _, f := range []string{zilNativeFile, zilETHFile, zilBSCFile} {
		assertReplaced(t, f, before[f], after[f], `"is_active":true`, `"is_active":false`, 1)
	}

	u = newUpdater(root, "ZIL", "Zilliqa")
	u.deactivate = true
	if err := u.run(false); err == nil || !strings.Contains(err.Error(), "no active legacy files") {
		t.Errorf("second -deactivate: err = %v", err)
	}
}

func TestUpdateDeactivateDeleteV3(t *testing.T) {
	root := copyRepo(t)
	u := newUpdater(root, "CHI", "Xaya")
	u.deactivate, u.deleteV3 = true, true
	before, after := runUpdate(t, root, u)

	f := "cryptocurrency/sygna:0x800001fd.json"
	assertChanged(t, before, after, "cryptocurrency_v3/CHI.json", f)
	assertReplaced(t, f, before[f], after[f], `"is_active":true`, `"is_active":false`, 1)
	if strings.Contains(after["cryptocurrency_v3/CHI.json"], "Xaya") || !strings.Contains(after["cryptocurrency_v3/CHI.json"], "Chi Gastoken") {
		t.Errorf("CHI.json should keep only Chi Gastoken:\n%s", after["cryptocurrency_v3/CHI.json"])
	}

	root = copyRepo(t)
	u = newUpdater(root, "ZIL", "Zilliqa")
	u.deactivate, u.deleteV3 = true, true
	before, after = runUpdate(t, root, u)
	assertChanged(t, before, after, "cryptocurrency_v3/ZIL.json", zilNativeFile, zilETHFile, zilBSCFile)
	if _, ok := after["cryptocurrency_v3/ZIL.json"]; ok {
		t.Error("ZIL.json should be deleted")
	}
}

func TestUpdateDryRunWritesNothing(t *testing.T) {
	root := copyRepo(t)
	before := snapshot(t, root)
	stdout := os.Stdout
	os.Stdout, _ = os.Open(os.DevNull)
	defer func() { os.Stdout = stdout }()

	u := newUpdater(root, "MV", "GensoKishi Metaverse")
	u.setName, u.setSymbol, u.setCoingeckoID = "Genso", "GENSO", "genso"
	u.setAddress = [][2]string{{"polygon", "0xa3c322ad15218fbfaed26ba7f616249f7705d999"}}
	if err := u.run(true); err != nil {
		t.Fatal(err)
	}
	if got := changed(before, snapshot(t, root)); len(got) != 0 {
		t.Errorf("dry run changed %v", got)
	}
}

func TestUpdateRejectsWithoutWriting(t *testing.T) {
	tests := []struct {
		name    string
		symbol  string
		cname   string
		set     func(u *updater)
		wantErr string
	}{
		{"unknown name", "ZIL", "Nope", func(u *updater) { u.setName = "X" }, `has no currency named "Nope"`},
		{"unknown symbol", "NOPE", "Nope", func(u *updater) { u.setName = "X" }, "no currency with symbol NOPE"},
		{"several with the same name", "SPA", "Sperax", func(u *updater) { u.setName = "X" }, "pass -uuid to choose one"},
		{"nothing to update", "ZIL", "Zilliqa", func(u *updater) {}, "nothing to update"},
		{"-delete-v3 alone", "ZIL", "Zilliqa", func(u *updater) { u.deleteV3 = true }, "only works together with -deactivate"},
		{"-deactivate with another change", "ZIL", "Zilliqa", func(u *updater) { u.deactivate, u.setName = true, "X" }, "cannot be combined"},
		{"same chain set and removed", "ZIL", "Zilliqa", func(u *updater) {
			u.setAddress = [][2]string{{"ethereum", tstETH}}
			u.removePlatforms = []string{"ethereum"}
		}, "given to both"},
		{"invalid symbol", "ZIL", "Zilliqa", func(u *updater) { u.setSymbol = "../X" }, "invalid -set-symbol"},
		{"same CoinGecko ID", "ZIL", "Zilliqa", func(u *updater) { u.setCoingeckoID = "zilliqa" }, "already uses CoinGecko ID"},
		{"CoinGecko ID of another currency", "ZIL", "Zilliqa", func(u *updater) { u.setCoingeckoID = "chimaera" }, "already mapped to currency"},
		{"name taken in the file", "CHI", "Chi Gastoken", func(u *updater) { u.setName = "xaya" }, "already has a currency named"},
		{"same name and symbol", "ZIL", "Zilliqa", func(u *updater) { u.setName = "Zilliqa" }, "already"},
		{"address on a missing chain", "ZIL", "Zilliqa", func(u *updater) { u.setAddress = [][2]string{{"polygon", tstETH}} }, "has no polygon platform"},
		{"address on a native chain", "ZIL", "Zilliqa", func(u *updater) { u.setAddress = [][2]string{{"zilliqa", "zil1abc"}} }, "native"},
		{"address unchanged", "ZIL", "Zilliqa", func(u *updater) {
			u.setAddress = [][2]string{{"ethereum", "0x05F4A42E251F2D52B8ED15E9FEDAACFCEF1FAD27"}}
		}, "already uses address"},
		{"address of another currency", "ZIL", "Zilliqa", func(u *updater) {
			u.setAddress = [][2]string{{"ethereum", "0xae788f80f2756a86aa2f410c651f2af83639b95b"}}
		}, "already used by MV"},
		{"invalid address", "ZIL", "Zilliqa", func(u *updater) { u.setAddress = [][2]string{{"ethereum", "0x12"}} }, "expected 0x followed by 40 hex"},
		{"remove a missing chain", "ZIL", "Zilliqa", func(u *updater) { u.removePlatforms = []string{"polygon"} }, "has no polygon platform"},
		{"remove the last chain", "CHI", "Chi Gastoken", func(u *updater) { u.removePlatforms = []string{"ethereum"} }, "use -deactivate"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := copyRepo(t)
			before := snapshot(t, root)
			u := newUpdater(root, tt.symbol, tt.cname)
			tt.set(u)
			err := u.run(false)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.wantErr)
			}
			if got := changed(before, snapshot(t, root)); len(got) != 0 {
				t.Errorf("files changed after error: %v", got)
			}
		})
	}
}

func TestUpdateRollsBackOnFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	root := copyRepo(t)
	before := snapshot(t, root)
	dir := filepath.Join(root, "id_mapping")
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o755)

	u := newUpdater(root, "ZIL", "Zilliqa")
	u.setAddress = [][2]string{{"ethereum", tstETH}}
	if err := u.run(false); err == nil || !strings.Contains(err.Error(), "all changes rolled back") {
		t.Fatalf("err = %v, want a rolled-back write failure", err)
	}
	if got := changed(before, snapshot(t, root)); len(got) != 0 {
		t.Errorf("files changed after rollback: %v", got)
	}
}

func TestWriteAllRestoresDeletedFiles(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	root := copyRepo(t)
	before := snapshot(t, root)
	dir := filepath.Join(root, "id_mapping")
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o755)

	r := newRepo(root)
	r.stageDelete("cryptocurrency_v3/MV.json")
	r.stage(o2nFile, []byte("{}"))
	if err := r.writeAll(); err == nil || !strings.Contains(err.Error(), "all changes rolled back") {
		t.Fatalf("err = %v, want a rolled-back write failure", err)
	}
	if got := changed(before, snapshot(t, root)); len(got) != 0 {
		t.Errorf("files changed after rollback: %v", got)
	}
}

// TestReplayXAUTAddressChange replays 3e19eee (SYGN-18577), which moved Tether
// Gold on Ethereum to a new contract by hand. The new legacy file and the
// new_to_old line must match that commit; unlike it, the old legacy ID is kept
// as inactive instead of being deleted.
func TestReplayXAUTAddressChange(t *testing.T) {
	root := copyFixture(t, "xaut")
	oldID := "sygna:0x8000003c.0x4922a015c4407f87432b179bb209e125432e4a2a"
	newID := "sygna:0x8000003c.0x68749665ff8d2d112fa859aa293f07a622782f38"
	oldFile := "cryptocurrency/" + oldID + ".json"
	newFile := "cryptocurrency/sygna-0x8000003c.0x68749665ff8d2d112fa859aa293f07a622782f38.json"

	u := newUpdater(root, "XAUT", "Tether Gold")
	u.setAddress = [][2]string{{"ethereum", "0x68749665FF8D2d112Fa859AA293F07A622782F38"}}
	before, after := runUpdate(t, root, u)

	assertChanged(t, before, after, "cryptocurrency_v3/XAUT.json", newFile, oldFile, cgFile, n2oFile, o2nFile)
	// git show 3e19eee:cryptocurrency/sygna:0x8000003c.0x68749665ff8d2d112fa859aa293f07a622782f38.json
	want := `{"currency_id":"sygna:0x8000003c.0x68749665ff8d2d112fa859aa293f07a622782f38","currency_name":"Tether Gold","currency_symbol":"XAUT","is_active":true,"addr_extra_info":[],"platform":{"name":"Ethereum","symbol":"ETH","token_address":"0x68749665ff8d2d112fa859aa293f07a622782f38","id":"sygna:0x8000003c"}}`
	if after[newFile] != want {
		t.Errorf("new legacy file:\n%s\nwant (from 3e19eee):\n%s", after[newFile], want)
	}
	// 3e19eee: "ethereum": "sygna:0x8000003c.0x68749665ff8d2d112fa859aa293f07a622782f38"
	assertReplaced(t, n2oFile, before[n2oFile], after[n2oFile], oldID, newID, 1)
	assertReplaced(t, oldFile, before[oldFile], after[oldFile], `"is_active":true`, `"is_active":false`, 1)
	assertAppended(t, o2nFile, before[o2nFile], after[o2nFile], "  \""+newID+"\": \"12503e04-6b38-4459-8b7b-b160bd3289bf\"", '}')
	assertAppended(t, cgFile, before[cgFile], after[cgFile], "    {\n        \"currency_id\": \""+newID+"\",\n        \"coingecko_id\": \"tether-gold\"\n    }", ']')
}
