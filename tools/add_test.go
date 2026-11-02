package main

import (
	"io"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"
)

// testdata/repo is a small subset of the real data (ZIL, CHI, SPA, MV) with the
// real files' formatting: 2- or 4-space indent and no trailing newline.

func TestMain(m *testing.M) {
	log.SetOutput(io.Discard)
	os.Exit(m.Run())
}

// copyRepo copies testdata/repo into a temp dir and returns its path.
func copyRepo(t *testing.T) string {
	t.Helper()
	return copyFixture(t, "repo")
}

// copyFixture copies testdata/<name> into a temp dir and returns its path.
func copyFixture(t *testing.T, name string) string {
	t.Helper()
	src := filepath.Join("testdata", name)
	dst := t.TempDir()
	err := filepath.WalkDir(src, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(src, p)
		if d.IsDir() {
			return os.MkdirAll(filepath.Join(dst, rel), 0o755)
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		return os.WriteFile(filepath.Join(dst, rel), data, 0o644)
	})
	if err != nil {
		t.Fatal(err)
	}
	return dst
}

// snapshot returns every file under root, keyed by slash-separated path.
func snapshot(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		data, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, p)
		files[filepath.ToSlash(rel)] = string(data)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// changed lists the files added, modified or removed between two snapshots.
func changed(before, after map[string]string) []string {
	var out []string
	for k, v := range after {
		if b, ok := before[k]; !ok || b != v {
			out = append(out, k)
		}
	}
	for k := range before {
		if _, ok := after[k]; !ok {
			out = append(out, k)
		}
	}
	sort.Strings(out)
	return out
}

// newAdder builds an adder the way main does after flag parsing.
func newAdder(root, symbol, name, coingeckoID, uuid string, platforms ...string) *adder {
	a := &adder{
		repo:        newRepo(root),
		symbol:      strings.ToUpper(symbol),
		name:        name,
		coingeckoID: coingeckoID,
		uuid:        uuid,
		overrides:   map[string]string{},
	}
	for _, p := range platforms {
		key, addr, _ := strings.Cut(p, ":")
		a.rawPlatforms = append(a.rawPlatforms, [2]string{key, addr})
	}
	return a
}

// assertAppended checks that after is before with entry inserted before the
// closing bracket, and nothing else changed.
func assertAppended(t *testing.T, file, before, after, entry string, closing byte) {
	t.Helper()
	i := strings.LastIndexByte(before, closing)
	want := before[:i-1] + ",\n" + entry + "\n" + before[i:]
	if after != want {
		t.Errorf("%s:\n got tail %q\nwant tail %q", file, tail(after), tail(want))
	}
}

func tail(s string) string {
	if len(s) > 300 {
		return "…" + s[len(s)-300:]
	}
	return s
}

const (
	tstUUID = "11111111-2222-4333-8444-555555555555"
	tstETH  = "0xAbCdEf0000000000000000000000000000000001"
	tstBSC  = "0xabcdef0000000000000000000000000000000002"
	zilUUID = "0e121e27-f8a5-441c-9607-99f9cad1795c"
)

func TestAddNewCurrency(t *testing.T) {
	root := copyRepo(t)
	before := snapshot(t, root)

	a := newAdder(root, "TST", "Test Token", "test-token", tstUUID,
		"ethereum:"+tstETH, "binance-smart-chain:"+tstBSC)
	if err := a.run(false); err != nil {
		t.Fatal(err)
	}
	after := snapshot(t, root)

	ethID := "sygna:0x8000003c." + strings.ToLower(tstETH)
	bscID := "sygna:0x8000232e." + tstBSC
	ethFile := "cryptocurrency/sygna-0x8000003c." + strings.ToLower(tstETH) + ".json"
	bscFile := "cryptocurrency/sygna-0x8000232e." + tstBSC + ".json"
	wantChanged := []string{
		"coingecko_id/mapping.json",
		"coingecko_id/mapping_v3.json",
		bscFile,
		ethFile,
		"cryptocurrency_v3/TST.json",
		"id_mapping/new_to_old_id_mapping.json",
		"id_mapping/old_to_new_id_mapping.json",
	}
	sort.Strings(wantChanged)
	if got := changed(before, after); strings.Join(got, "\n") != strings.Join(wantChanged, "\n") {
		t.Fatalf("changed files:\n%s\nwant:\n%s", strings.Join(got, "\n"), strings.Join(wantChanged, "\n"))
	}

	wantV3 := `[
  {
    "id": "` + tstUUID + `",
    "symbol": "TST",
    "name": "Test Token",
    "platforms": {
      "ethereum": {
        "token_address": "` + strings.ToLower(tstETH) + `"
      },
      "binance-smart-chain": {
        "token_address": "` + tstBSC + `"
      }
    }
  }
]`
	if got := after["cryptocurrency_v3/TST.json"]; got != wantV3 {
		t.Errorf("TST.json:\n%s\nwant:\n%s", got, wantV3)
	}

	wantETH := `{"currency_id":"` + ethID + `","currency_name":"Test Token","currency_symbol":"TST","is_active":true,"addr_extra_info":[],"platform":{"name":"Ethereum","symbol":"ETH","token_address":"` + strings.ToLower(tstETH) + `","id":"sygna:0x8000003c"}}`
	if got := after[ethFile]; got != wantETH {
		t.Errorf("%s:\n%s\nwant:\n%s", ethFile, got, wantETH)
	}
	wantBSC := `{"currency_id":"` + bscID + `","currency_name":"Test Token","currency_symbol":"TST","is_active":true,"addr_extra_info":[],"platform":{"name":"Binance Smart Chain","symbol":"BSC","token_address":"` + tstBSC + `","id":"sygna:0x8000232e"}}`
	if got := after[bscFile]; got != wantBSC {
		t.Errorf("%s:\n%s\nwant:\n%s", bscFile, got, wantBSC)
	}

	f := "coingecko_id/mapping_v3.json"
	assertAppended(t, f, before[f], after[f], "  {\n    \"currency_id\": \""+tstUUID+"\",\n    \"coingecko_id\": \"test-token\"\n  }", ']')
	f = "coingecko_id/mapping.json"
	assertAppended(t, f, before[f], after[f],
		"    {\n        \"currency_id\": \""+ethID+"\",\n        \"coingecko_id\": \"test-token\"\n    },\n"+
			"    {\n        \"currency_id\": \""+bscID+"\",\n        \"coingecko_id\": \"test-token\"\n    }", ']')
	f = "id_mapping/new_to_old_id_mapping.json"
	assertAppended(t, f, before[f], after[f],
		"  \""+tstUUID+"\": {\n    \"ethereum\": \""+ethID+"\",\n    \"binance-smart-chain\": \""+bscID+"\"\n  }", '}')
	f = "id_mapping/old_to_new_id_mapping.json"
	assertAppended(t, f, before[f], after[f],
		"  \""+ethID+"\": \""+tstUUID+"\",\n  \""+bscID+"\": \""+tstUUID+"\"", '}')
}

func TestAddPlatformToExistingCurrency(t *testing.T) {
	root := copyRepo(t)
	before := snapshot(t, root)

	addr := "0x05f4a42e251f2d52b8ed15e9fedaacfcef1fad99"
	a := newAdder(root, "ZIL", "Zilliqa", "", "", "polygon:"+addr)
	if err := a.run(false); err != nil {
		t.Fatal(err)
	}
	after := snapshot(t, root)

	if _, ok := before["coingecko_id/mapping_v3.json"]; !ok {
		t.Fatal("fixture is missing mapping_v3.json")
	}
	if before["coingecko_id/mapping_v3.json"] != after["coingecko_id/mapping_v3.json"] {
		t.Error("mapping_v3.json changed for an existing currency")
	}

	v3 := after["cryptocurrency_v3/ZIL.json"]
	wantTail := `      "binance-smart-chain": {
        "token_address": "0xb86abcb37c3a4b64f74f59301aff131a1becc787"
      },
      "polygon": {
        "token_address": "` + addr + `"
      }
    }
  }
]`
	if !strings.HasSuffix(v3, wantTail) {
		t.Errorf("ZIL.json should end with the new polygon platform:\n%s", v3)
	}

	id := "sygna:0x800003c6." + addr
	f := "id_mapping/new_to_old_id_mapping.json"
	wantN2O := strings.Replace(before[f],
		"\"binance-smart-chain\": \"sygna:0x8000232e.0xb86abcb37c3a4b64f74f59301aff131a1becc787\"\n  }",
		"\"binance-smart-chain\": \"sygna:0x8000232e.0xb86abcb37c3a4b64f74f59301aff131a1becc787\",\n    \"polygon\": \""+id+"\"\n  }", 1)
	if after[f] != wantN2O {
		t.Errorf("%s: ZIL object was not extended in place:\n%s", f, after[f])
	}
	f = "coingecko_id/mapping.json"
	assertAppended(t, f, before[f], after[f], "    {\n        \"currency_id\": \""+id+"\",\n        \"coingecko_id\": \"zilliqa\"\n    }", ']')
	f = "id_mapping/old_to_new_id_mapping.json"
	assertAppended(t, f, before[f], after[f], "  \""+id+"\": \""+zilUUID+"\"", '}')
}

func TestAddNativePlatformIsV3Only(t *testing.T) {
	root := copyRepo(t)
	before := snapshot(t, root)

	a := newAdder(root, "TST", "Test Token", "test-token", tstUUID, "zilliqa:native")
	if err := a.run(false); err != nil {
		t.Fatal(err)
	}
	got := changed(before, snapshot(t, root))
	want := []string{"coingecko_id/mapping_v3.json", "cryptocurrency_v3/TST.json"}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Errorf("changed files = %v, want %v", got, want)
	}
}

func TestAddDryRunWritesNothing(t *testing.T) {
	root := copyRepo(t)
	before := snapshot(t, root)

	stdout := os.Stdout
	os.Stdout, _ = os.Open(os.DevNull)
	defer func() { os.Stdout = stdout }()

	a := newAdder(root, "TST", "Test Token", "test-token", tstUUID, "ethereum:"+tstETH)
	if err := a.run(true); err != nil {
		t.Fatal(err)
	}
	if got := changed(before, snapshot(t, root)); len(got) != 0 {
		t.Errorf("dry run changed %v", got)
	}
}

func TestAddRejectsWithoutWriting(t *testing.T) {
	tests := []struct {
		name    string
		adder   func(root string) *adder
		wantErr string
	}{
		{"platform already exists", func(r string) *adder {
			return newAdder(r, "ZIL", "Zilliqa", "", "", "ethereum:0x05f4a42e251f2d52b8ed15e9fedaacfcef1fad27")
		}, `platform "ethereum" already exists`},
		{"address used by another currency", func(r string) *adder {
			return newAdder(r, "ZIL", "Zilliqa", "", "", "polygon:0xa3c322ad15218fbfaed26ba7f616249f7705d945")
		}, "is already used by MV"},
		{"new currency without CoinGecko ID", func(r string) *adder {
			return newAdder(r, "TST", "Test Token", "", "", "ethereum:"+tstETH)
		}, "--coingecko-id is required"},
		{"CoinGecko ID used by another currency", func(r string) *adder {
			return newAdder(r, "TST", "Test Token", "zilliqa", "", "ethereum:"+tstETH)
		}, "already mapped to currency " + zilUUID},
		{"name differs only in case", func(r string) *adder {
			return newAdder(r, "ZIL", "zilliqa", "", "", "polygon:"+tstETH)
		}, "differs only in case"},
		{"several currencies with the same name", func(r string) *adder {
			return newAdder(r, "SPA", "Sperax", "", "", "polygon:"+tstETH)
		}, "pass -uuid to choose one"},
		{"invalid EVM address", func(r string) *adder {
			return newAdder(r, "TST", "Test Token", "test-token", "", "ethereum:0x123")
		}, "expected 0x followed by 40 hex characters"},
		{"address escaping the directory", func(r string) *adder {
			return newAdder(r, "TST", "Test Token", "test-token", "", "zilliqa:../../etc")
		}, "must not contain"},
		{"chain without an existing legacy token", func(r string) *adder {
			return newAdder(r, "TST", "Test Token", "test-token", "", "kaia:"+tstETH)
		}, "no existing legacy token on kaia"},
		{"platform given twice", func(r string) *adder {
			return newAdder(r, "TST", "Test Token", "test-token", "", "ethereum:"+tstETH, "ethereum:"+tstBSC)
		}, `platform "ethereum" given twice`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := copyRepo(t)
			before := snapshot(t, root)
			err := tt.adder(root).run(false)
			if err == nil || !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("err = %v, want it to contain %q", err, tt.wantErr)
			}
			if got := changed(before, snapshot(t, root)); len(got) != 0 {
				t.Errorf("files changed after error: %v", got)
			}
		})
	}
}

func TestWriteAllRollsBackOnFailure(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root ignores directory permissions")
	}
	root := copyRepo(t)
	before := snapshot(t, root)

	// id_mapping is written last; making it read-only fails the batch after
	// the v3, legacy and coingecko files are already written.
	dir := filepath.Join(root, "id_mapping")
	if err := os.Chmod(dir, 0o555); err != nil {
		t.Fatal(err)
	}
	defer os.Chmod(dir, 0o755)

	a := newAdder(root, "TST", "Test Token", "test-token", tstUUID, "ethereum:"+tstETH)
	err := a.run(false)
	if err == nil || !strings.Contains(err.Error(), "all changes rolled back") {
		t.Fatalf("err = %v, want a rolled-back write failure", err)
	}
	if got := changed(before, snapshot(t, root)); len(got) != 0 {
		t.Errorf("files changed after rollback: %v", got)
	}
}
