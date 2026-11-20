# Currency Management Tools

## 📋 Overview

The `add_currency` tool (`go run .` in `tools/`) adds a currency, or new platforms for an existing currency, across **both v3 and legacy formats** in one command. With `-update` it changes, removes or delists an existing currency instead (see Update mode below). When adding, it writes up to 7 files:

| File | When |
|---|---|
| `cryptocurrency_v3/<SYMBOL>.json` | always |
| `coingecko_id/mapping_v3.json` | new currency only |
| `cryptocurrency/sygna-<sygna-id>.<address>.json` | one per token platform |
| `coingecko_id/mapping.json` | one entry per token platform |
| `id_mapping/new_to_old_id_mapping.json` | token platforms |
| `id_mapping/old_to_new_id_mapping.json` | token platforms |

Mapping files are edited append-only (existing bytes untouched, no trailing newline), and every change is validated in memory before anything is written — if any check fails, nothing is written.

---

## 🚀 Quick Start

No Go installed? Use `./add_currency.sh` with the same flags — it downloads Go into `~/.cache/sygna-bridge-assembly/` on first run.

```bash
cd tools

# New currency on two chains
go run . -symbol NIGHT -name Midnight --coingecko-id midnight-3 \
  -platform binance-smart-chain:0xfe930c2d63aed9b82fc4dbc801920dd2c1a3224f \
  -platform cardano:0691b2fecca1ac4f53cb6dfb00b7013e561d1f34403b957cbb5af1fa4e49474854

# Add a platform to an existing currency (CoinGecko ID is reused from mapping_v3.json)
go run . -symbol USDT -name Tether -platform polygon:0xc2132d05d31c914a87c6611c10748aeb04b58e8f

# Preview without writing
go run . ... -dry-run
```

---

## 📖 Flags

| Flag | Description |
|---|---|
| `-symbol` | Currency symbol (required) |
| `-name` | Currency name (required). Matched exactly against existing entries in the symbol file; a case-only difference is an error |
| `-platform KEY:ADDRESS` | Repeatable. `KEY` is the v3 platform key (e.g. `ethereum`, `binance-smart-chain`, `polygon`). Native must be explicit: `KEY:native` |
| `--token-address` | Old single-platform syntax: `-platform polygon --token-address 0x...` |
| `--coingecko-id` | Required for a new currency; optional (must match) when adding platforms. If an existing currency has no `mapping_v3.json` entry, passing it adds one |
| `-allow-shared-coingecko-id` | Allow a new currency to reuse a CoinGecko ID already mapped to another currency |
| `-sygna-id KEY=sygna:...` | Override the legacy Sygna ID for a platform |
| `-uuid` | Use a fixed UUID for a new currency instead of generating one |
| `-dry-run` | Print the plan only |
| `-root` | Repository root (default `..`) |

### How to Find CoinGecko ID

Check CoinGecko's site, e.g. https://www.coingecko.com/en/coins/bitcoin, and find the `API ID` field in the `Info` block. Don't trust the ticker alone — confirm the contract addresses match.

---

## 🎯 Behavior

- **Same symbol + name exists** → adds platforms to it (error if a platform already exists).
- **Otherwise** → creates a new currency with a new UUID (the symbol file may hold several currencies).
- **Addresses**: v3 always lowercased. Legacy keeps the original case, except EVM addresses (`0x` + 40 hex) which are lowercased.
- **Legacy Sygna ID** per platform: `-sygna-id` override → built-in table for common chains → unique match in `blockchain_name/mapping.json` (hyphens → underscores). Ambiguous or missing → error asking for `-sygna-id`.
- **Legacy `platform.name`/`symbol`** are copied from existing legacy tokens on the same chain. If the chain has no token yet, the tool stops — add that platform manually.
- **Native platforms** only get the v3 entry; legacy native files must be added manually.
- **Duplicates**: refuses if the platform already exists on the currency (case-insensitive), the same address is already used on that platform by any currency, the legacy ID is already in `old_to_new_id_mapping.json`, or a legacy file exists under either `sygna-` or `sygna:` naming.
- **Input validation**: symbols are limited to `A-Z 0-9 $ . _ -`; addresses may not contain `/`, `\`, `..` or whitespace; EVM chains require `0x` + 40 hex. Nothing is ever written outside the repository.
- **Writes**: each file is replaced atomically (temp file + rename). If any write fails, files already written are restored, so the repo is never left half-updated.
- **Tests**: `go test ./...` runs the tool against the fixtures in `testdata/` (copies of real data, never deployed).

---

## ✏️ Update mode

`-update` finds an existing currency by `-symbol` + `-name` (current values; `-uuid` when several share the name) and changes it. It never creates a currency.

| Flag | Change |
|---|---|
| `-set-address KEY:ADDRESS` | ① New token address on a chain (repeatable). Adds a new legacy ID and file; the old legacy ID stays, marked `"is_active": false` |
| `-set-name NAME` / `-set-symbol SYM` | ② New name / symbol in v3 and in every legacy file of the currency. A new symbol moves the currency to `cryptocurrency_v3/<SYM>.json`; an emptied symbol file is deleted |
| `-set-coingecko-id ID` | ③ New CoinGecko ID in `mapping_v3.json` and for every legacy ID in `mapping.json` |
| `-remove-platform KEY` | ④ Removes a chain from v3 and `new_to_old`; its legacy file becomes inactive (repeatable; the last chain cannot be removed) |
| `-deactivate` | ⑤ Delists the currency: every legacy file becomes inactive, mappings are kept. Cannot be combined with other changes |
| `-delete-v3` | With `-deactivate`: also removes the currency from `cryptocurrency_v3` |

```bash
# ① New contract on Ethereum (placeholder address)
go run . -update -symbol XAUT -name 'Tether Gold' -set-address ethereum:0x0000000000000000000000000000000000000001 -dry-run

# ② + ③ Rename, new symbol and new CoinGecko ID (placeholder values)
go run . -update -symbol ZIL -name Zilliqa -set-name 'Zilliqa Coin' -set-symbol ZILQ -set-coingecko-id zilliqa-coin -dry-run

# ⑤ Delist and remove from v3
go run . -update -symbol ZIL -name Zilliqa -deactivate -delete-v3 -dry-run
```

- **Old legacy IDs are never deleted or renamed** — Bridge may still resolve them, so they stay in every mapping file with `"is_active": false`. They cannot be re-activated by the tool: `-set-address` back to an old address fails with `already mapped`.
- **Legacy files are edited field by field**, keeping each file's formatting; mapping files only change the affected lines.
- **Platform keys** are matched case-insensitively (v3 has e.g. `"Kaia"` where `new_to_old` has `"kaia"`).
- **Native platforms**: ① refuses native chains (change them manually); ② and ⑤ edit native legacy files like any other.
- **Dry run** lists each file as `add` / `modify` / `delete`, then the currency before → after.
- **Checks before writing**: `-symbol` / `-set-symbol` are valid symbols; the currency and chain exist; a new address is valid and unused on that chain; a new name is unique in the symbol file; a new CoinGecko ID is not used by another currency (unless `-allow-shared-coingecko-id`). After staging, the tool cross-checks the v3, legacy and mapping entries it changed and writes nothing if anything disagrees.
- **Known formats**: `cryptocurrency_v3/GRAM.json` is a single object rather than an array, so updating GRAM fails safely; `JPYSC.json` is reformatted on its first update.
