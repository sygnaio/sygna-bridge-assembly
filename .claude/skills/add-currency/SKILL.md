---
name: add-currency
description: Add a new cryptocurrency, or a new platform/chain for an existing one, to the Sygna Bridge assembly data from a ClickUp ticket (e.g. "/add-currency https://app.clickup.com/t/<workspace>/z8qgvc4q1b") — reads the spec from ClickUp, verifies it on CoinGecko, writes v3/legacy/mapping files via tools/add_currency.sh, and opens a PR to dev. Use whenever someone asks to add/list a token, coin, or contract on a chain.
---

# Add currency / platform

The person running this is often a **PM, not an engineer**. Talk in plain language in the user's language, never ask them to run commands or edit files, and don't show raw diffs unless asked. They decide *what* to list; you handle *how*.

All data changes go through `tools/add_currency.sh` (wraps `add_currency.go`; downloads Go into a cache on first run if it isn't installed, so the user installs nothing). Never hand-edit the JSON data files.

## 1. Read the ticket from ClickUp

The argument is a ClickUp task URL (`https://app.clickup.com/t/<workspace>/<task-id>`) or task ID; if none was given, ask for it. Use the `clickup` MCP tools to fetch the task (pass the workspace ID from the URL): name, description, custom fields, and comments (later comments may correct the spec).

`<TICKET>` below is the ClickUp task ID (e.g. `z8qgvc4q1b`) — used for the branch and commit message.

If the ClickUp tools are missing or not signed in, tell the user: type `/mcp`, choose **clickup**, sign in with their ClickUp account, then retry.

Extract, from whatever form the spec uses:
- Currency name and symbol
- Each chain + contract address (or native)
- CoinGecko ID or link, if given

Specs are free-form. Don't guess missing pieces — if a chain or address is unclear, ask the user.

## 2. Verify on CoinGecko

Tickers collide — never trust the symbol alone.

```bash
curl -s "https://api.coingecko.com/api/v3/search?query=<name>" | python3 -c 'import json,sys;[print(c["id"],c["symbol"],c.get("market_cap_rank")) for c in json.load(sys.stdin)["coins"][:8]]'
curl -s "https://api.coingecko.com/api/v3/coins/<id>?localization=false&tickers=false&market_data=false" | python3 -c 'import json,sys;d=json.load(sys.stdin);print(d["id"],d["symbol"],d["name"]);print(json.dumps(d["platforms"],indent=1))'
```

Every ticket address must appear in the coin's `platforms` (case-insensitive). If one doesn't, or several coins fit, **stop and explain** what doesn't match — the ticket may need fixing. Continue with an unlisted address only if the user explicitly confirms the requester wants it, and note "not listed on CoinGecko, confirmed by requester" in the PR body.

Map each chain to this repo's v3 platform key (e.g. `ethereum`, `binance-smart-chain`, `polygon`, `arbitrum`, `optimism`, `avalanche-c-chain`, `tron`, `solana`, `cardano`). These are not always CoinGecko's keys (`polygon`, not `polygon-pos`). Keys in use: `cat cryptocurrency_v3/*.json | grep -oE '^      "[a-z0-9.-]+": \{' | sort | uniq -c | sort -rn`.

## 3. Prepare the branch

```bash
git status --porcelain   # must be empty; otherwise stop and ask — never discard the user's changes
git fetch origin && git checkout -b feat/<TICKET> origin/dev
```

If the branch already exists, ask before reusing it.

## 4. Dry run and confirm

```bash
tools/add_currency.sh -symbol <SYM> -name "<Name>" --coingecko-id <cg-id> \
  -platform <key>:<address> [-platform <key2>:<address2> ...] -dry-run
```

Pass addresses in their original case. For an existing currency (same symbol + name), `--coingecko-id` may be omitted — it's reused.

Show the user a short summary — name, symbol, CoinGecko ID (with link), each chain + address, and whether it's a new currency or new chains for an existing one. **Wait for their confirmation** before writing.

On errors, explain in plain language:

| Error | Action |
|---|---|
| `already exists` / `already mapped` | Already listed — tell the user, stop |
| `cannot find Sygna ID` / `matches several Sygna IDs` | Look it up in `blockchain_name/mapping.json`; if one clear match, confirm and re-run with `-sygna-id <key>=sygna:...`, otherwise needs an engineer |
| `no existing legacy token on ...` | First token on this chain — needs an engineer; stop |
| native platform warning | Legacy native files are manual — needs an engineer |

## 5. Write, commit, open PR

```bash
tools/add_currency.sh <same args without -dry-run>
git add cryptocurrency cryptocurrency_v3 coingecko_id id_mapping
git status --short   # only data files should be staged
git commit -m "feat: <TICKET> add <Name> (<SYM>) currency"
git push -u origin feat/<TICKET>
```

Open the PR against `dev` with `gh pr create --base dev` if `gh` is installed and logged in; otherwise give the user this link to open it: `https://github.com/sygnaio/sygna-bridge-assembly/compare/dev...feat/<TICKET>?expand=1`. PR title: `feat: <TICKET> add <Name> (<SYM>) ...`. PR body: ClickUp task link, CoinGecko link, chain → address table, and any confirmed exceptions.

Finally, comment the PR link on the ClickUp task (via the ClickUp MCP) and tell the user an engineer will review it.
