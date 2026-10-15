# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What This Repository Is

A cryptocurrency/token metadata data store for Sygna Bridge. It contains structured JSON files describing cryptocurrencies, their supported platforms, and associated metadata. There is no application code to run — CI validation happens in a separate private repository (`sygna-bridge-assembly-handler`).

## Adding a New Currency or Platform

A complete addition touches **7 files** (both v3 and legacy formats). `tools/add_currency.go` writes all of them in one command (`tools/add_currency.sh` runs it and downloads Go into a cache if it isn't installed). The `/add-currency <ClickUp task URL or ID>` skill (`.claude/skills/add-currency/SKILL.md`) wraps the full flow for PMs: reads the spec from ClickUp via the project's ClickUp MCP server (`.mcp.json`), verifies on CoinGecko, runs the tool, and opens the PR.

### Step 1 — Verify the CoinGecko ID first

Don't trust the symbol alone — multiple coins share tickers. Query `https://api.coingecko.com/api/v3/search?query=<name>` and pick by market cap rank, then confirm contract addresses match the ticket via `https://api.coingecko.com/api/v3/coins/<id>` (e.g. NIGHT resolves to `midnight-3`, not `midnight`).

### Step 2 — Run the Go tool (all 7 files)

```bash
cd tools

# New currency, one -platform KEY:ADDRESS per chain (addresses in original case)
go run add_currency.go -symbol NIGHT -name Midnight --coingecko-id midnight-3 \
  -platform binance-smart-chain:0xfe930c2d63aed9b82fc4dbc801920dd2c1a3224f \
  -platform cardano:0691b2fecca1ac4f53cb6dfb00b7013e561d1f34403b957cbb5af1fa4e49474854

# Add a platform to an existing currency (matched by symbol + exact name; CoinGecko ID reused)
go run add_currency.go -symbol USDT -name Tether -platform polygon:0xc2132d05d31c914a87c6611c10748aeb04b58e8f

# Append -dry-run to preview
```

Platform keys are this repo's v3 keys (`polygon`, `arbitrum`, `optimism`, …), not always CoinGecko's. See `tools/README.md` for all flags (`-sygna-id` override, `-uuid`, `-dry-run`). The tool validates everything in memory and writes nothing if any check fails. It does **not** handle native-chain legacy files or chains with no existing legacy token — those are manual (Step 3 describes the format).

### Step 3 — What the tool writes (legacy format reference)

v3: `cryptocurrency_v3/<SYMBOL>.json` (addresses always lowercased, platform key order preserved) and, for a new currency, `coingecko_id/mapping_v3.json`.

Legacy, for **each token platform**, using the platform's Sygna ID (see reference table below):

1. **`cryptocurrency/sygna-<platform-id>.<token_address>.json`** — a **dash** after `sygna` for new files. (The directory is mixed: most older files use a colon, e.g. `sygna:0x8000003c.<addr>.json`; don't rename them.) The `platform` object (`name`/`symbol`/`id`) matches existing token files on the same chain, e.g.:
   ```json
   {"currency_id":"sygna:0x8000232e.0xfe930c...","currency_name":"Midnight","currency_symbol":"NIGHT","is_active":true,"addr_extra_info":[],"platform":{"name":"Binance Smart Chain","symbol":"BSC","token_address":"0xfe930c...","id":"sygna:0x8000232e"}}
   ```
   Single line, no pretty-printing. EVM addresses lowercase; non-EVM as-is, keeping original case for case-sensitive chains (e.g. Solana base58 `2zMMhcVQ...` stays mixed-case in legacy even though v3 stores it lowercased). The legacy `currency_id` string is identical across this file and the three mapping files below.
2. **`coingecko_id/mapping.json`** — one entry per platform `currency_id`, all pointing at the same `coingecko_id`.
3. **`id_mapping/new_to_old_id_mapping.json`** — `"<uuid>": { "<platform-key>": "<legacy currency_id>", ... }` (one object with all platforms; adding a platform to an existing UUID extends its existing object).
4. **`id_mapping/old_to_new_id_mapping.json`** — `"<legacy currency_id>": "<uuid>"` per platform.

### Platform reference (common)

| Platform key (v3 / CoinGecko) | Sygna ID | Legacy `platform.name` | `platform.symbol` |
|---|---|---|---|
| `ethereum` | `sygna:0x8000003c` | Ethereum | ETH |
| `binance-smart-chain` | `sygna:0x8000232e` | Binance Smart Chain | BSC |
| `cardano` | `sygna:0x80000717` | Cardano | ADA |
| `solana` | `sygna:0x800001f5` | Solana | SOL |

For other chains: find the Sygna ID in `blockchain_name/mapping.json` (note: its values use underscores, e.g. `binance_smart_chain`, but v3 platform keys use hyphens; some newer chains have non-hex IDs like `sygna:hyperevm`, `sygna:abstract`), then copy `name`/`symbol` from any existing legacy token file with that ID prefix.

### Formatting gotchas

- `coingecko_id/mapping.json` uses 4-space indent; `mapping_v3.json` and `id_mapping/*` use 2-space; all of these mapping files have **no trailing newline** — append with a targeted edit at the end of the array, don't rewrite/reformat the whole file.
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

Tickets live in ClickUp. Use the ClickUp task ID (the `<id>` in `https://app.clickup.com/t/<workspace>/<id>`, e.g. `z8qgvc4q1b`): branch from `dev` as `feat/<task-id>` (or `fix/<task-id>`), commit messages `feat: <task-id> <summary>` / `fix: <task-id> <summary>`. Older history uses `SYGN-XXXXX`. Squash to a single commit before opening the PR against `dev`.

## CI Validation

PR validation runs automatically via `.github/workflows/checker.yml`. It clones the private `sygna-bridge-assembly-handler` repo and runs `npm run check`. There is no way to run this check locally without access to that repo.

Branches `master` and `test` use the matching handler branch; all other branches (including `dev`) use the handler's `dev` branch.
