# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Repository Is

A cryptocurrency/token metadata data store for Sygna Bridge. It contains structured JSON files describing cryptocurrencies, their supported platforms, and associated metadata. There is no application code to run — CI validation happens in a separate private repository (`sygna-bridge-assembly-handler`).

## Adding a New Currency or Platform

A complete addition touches **7 files** (both v3 and legacy formats). The Go tool only covers the first two; the rest are manual.

### Step 1 — Verify the CoinGecko ID first

Don't trust the symbol alone — multiple coins share tickers. Query `https://api.coingecko.com/api/v3/search?query=<name>` and pick by market cap rank, then confirm contract addresses match the ticket via `https://api.coingecko.com/api/v3/coins/<id>` (e.g. NIGHT resolves to `midnight-3`, not `midnight`).

### Step 2 — v3 files (Go tool)

```bash
cd tools

# Add a new currency (creates a new UUID and file entry)
go run add_currency.go -symbol BTC -name Bitcoin -platform bitcoin --token-address native --coingecko-id bitcoin

# Add a platform to an existing currency (matches by name; run once per extra platform)
go run add_currency.go -symbol USDT -name Tether -platform polygon --token-address 0xc2132d05d31c914a87c6611c10748aeb04b58e8f
```

The tool:
- Creates `cryptocurrency_v3/<SYMBOL>.json` if it doesn't exist
- Appends a new platform entry if the currency name already exists
- Generates a UUID for new currencies
- Appends to `coingecko_id/mapping_v3.json` when `--coingecko-id` is provided (only on currency creation — the platform-key must match CoinGecko's platform key, e.g. `binance-smart-chain`, `cardano`, `ethereum`)

### Step 3 — Legacy files (manual)

For **each platform**, using the platform's Sygna ID (see reference table below):

1. **`cryptocurrency/sygna-<platform-id>.<token_address>.json`** — note **dashes** in the filename (not colons). Copy the `platform` object (`name`/`symbol`/`id`) verbatim from an existing token file on the same chain, e.g.:
   ```json
   {"currency_id":"sygna:0x8000232e.0xfe930c...","currency_name":"Midnight","currency_symbol":"NIGHT","is_active":true,"addr_extra_info":[],"platform":{"name":"Binance Smart Chain","symbol":"BSC","token_address":"0xfe930c...","id":"sygna:0x8000232e"}}
   ```
   Single line, no pretty-printing. EVM addresses lowercase; non-EVM (e.g. Cardano policy ID) as-is.
2. **`coingecko_id/mapping.json`** — append one entry per platform `currency_id`, all pointing at the same `coingecko_id`.
3. **`id_mapping/new_to_old_id_mapping.json`** — add `"<uuid>": { "<platform-key>": "<legacy currency_id>", ... }` (one object with all platforms).
4. **`id_mapping/old_to_new_id_mapping.json`** — add `"<legacy currency_id>": "<uuid>"` per platform.

### Platform reference (common)

| Platform key (v3 / CoinGecko) | Sygna ID | Legacy `platform.name` | `platform.symbol` |
|---|---|---|---|
| `ethereum` | `sygna:0x8000003c` | Ethereum | ETH |
| `binance-smart-chain` | `sygna:0x8000232e` | Binance Smart Chain | BSC |
| `cardano` | `sygna:0x80000717` | Cardano | ADA |

For other chains: find the Sygna ID in `blockchain_name/mapping.json` (note: its values use underscores, e.g. `binance_smart_chain`, but v3 platform keys use hyphens), then copy `name`/`symbol` from any existing legacy token file with that ID prefix.

### Formatting gotchas

- `coingecko_id/mapping.json` uses 4-space indent; `mapping_v3.json` and `id_mapping/*` use 2-space; both mapping files have **no trailing newline** — append with a targeted edit at the end of the array, don't rewrite/reformat the whole file.
- Legacy `cryptocurrency/*.json` files are single-line compact JSON.

## Data Formats

### v3 (current) — `cryptocurrency_v3/<SYMBOL>.json`

```json
[
  {
    "id": "089dd571-dbcc-4c1b-9d0c-fd6c3bbf5a12",
    "symbol": "BTC",
    "name": "Bitcoin",
    "platforms": {
      "bitcoin": {
        "token_address": "native"
      }
    }
  }
]
```

- One file per symbol (uppercase), containing an array — multiple entries when the same symbol represents different tokens (e.g., wrapped versions on different chains)
- `token_address` is `"native"` for the chain's base currency, or a lowercase hex address for tokens

### Legacy — `cryptocurrency/<sygna-id>.json`

```json
{
  "currency_id": "sygna:0x80000bd6",
  "currency_name": "Hedera",
  "currency_symbol": "HBAR",
  "is_active": true,
  "addr_extra_info": ["tag"],
  "platform": null
}
```

- Filename uses the Sygna-formatted currency ID (e.g., `sygna-0x80000bd6.json`, dashes instead of colons)
- `addr_extra_info` lists extra fields required for a transaction address (e.g., `"tag"`, `"memo"`)
- `platform` is `null` for native chains, or a string for tokens on a parent chain

## Key Supporting Files

| File | Purpose |
|---|---|
| `coingecko_id/mapping_v3.json` | Maps UUID → CoinGecko ID for v3 currencies |
| `coingecko_id/mapping.json` | Maps legacy Sygna ID → CoinGecko ID |
| `blockchain_name/mapping.json` | Maps platform keys to human-readable blockchain names |
| `kyt_providers/elliptic.json` | Asset/blockchain info for Elliptic KYT integration |
| `id_mapping/new_to_old_id_mapping.json` | UUID → legacy Sygna ID cross-reference |
| `id_mapping/old_to_new_id_mapping.json` | Legacy Sygna ID → UUID cross-reference |

## Branch & Commit Convention

Branch from `dev` as `feat/SYGN-XXXXX` (or `fix/SYGN-XXXXX`). Commit messages follow `feat: SYGN-XXXXX <summary>` / `fix: SYGN-XXXXX <summary>`. Squash to a single commit before opening the PR against `dev`.

## CI Validation

PR validation runs automatically via `.github/workflows/checker.yml`. It clones the private `sygna-bridge-assembly-handler` repo and runs `npm run check`. There is no way to run this check locally without access to that repo.

Branches `master` and `test` use the matching handler branch; all other branches (including `dev`) use the handler's `dev` branch.
