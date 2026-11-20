# Test fixtures — not real data

`repo/` and `xaut/` are small copies of the assembly data used only by the Go tests in `tools/` (`go test ./...`). Each test copies it to a temp directory and runs the tool there, so these files are never changed and never shipped.

- **Not deployed.** `sygna-bridge-assembly-handler` only reads the top-level data folders (`cryptocurrency/`, `cryptocurrency_v3/`, `coingecko_id/`, `id_mapping/`, …), not `tools/testdata/`.
- **Not a currency listing.** Adding or editing files here does not add or change any currency.

## What is in `repo/`

A subset of the real data, copied as is on 2026-11-02, with the mapping files cut down to the same currencies and kept in the real format (2- or 4-space indent, no trailing newline):

| Symbol | Why it is here |
|---|---|
| ZIL | native chain plus Ethereum and BEP-20 tokens (`sygna-` file names) |
| CHI | one symbol file with two currencies; a `sygna:` file name and a native chain |
| SPA | three currencies with the same name (needs `-uuid`) |
| MV | a Polygon token, so tests can add a Polygon platform |
| BORA | v3 key `"Kaia"` but `new_to_old` key `"kaia"` (like KAIA, MBX and SSX in the real data) |

`blockchain_name/mapping.json` keeps only the chains these currencies use, plus `hyperevm` (a chain with no legacy token here).

## What is in `xaut/`

Tether Gold (XAUT) as it was before commit `3e19eee` (SYGN-18577), which changed its Ethereum contract by hand. `TestReplayXAUTAddressChange` replays that change with `-update -set-address` and compares the result with the commit.
