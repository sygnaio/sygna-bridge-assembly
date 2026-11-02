package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"strings"
)

// repeated collects a flag that may be passed multiple times.
type repeated []string

func (r *repeated) String() string     { return strings.Join(*r, ",") }
func (r *repeated) Set(v string) error { *r = append(*r, v); return nil }

func main() {
	var platformFlags, sygnaIDFlags repeated
	symbol := flag.String("symbol", "", "Currency symbol (required, e.g. BTC, USDT)")
	name := flag.String("name", "", "Currency name (required, e.g. Bitcoin, Tether)")
	flag.Var(&platformFlags, "platform", "Platform as KEY:ADDRESS or KEY:native (repeatable). KEY alone only together with --token-address")
	tokenAddress := flag.String("token-address", "", "Token address (or native) when a single -platform is given without :ADDRESS")
	coingeckoID := flag.String("coingecko-id", "", "CoinGecko ID (required for a new currency)")
	allowSharedCG := flag.Bool("allow-shared-coingecko-id", false, "Allow a new currency to use a CoinGecko ID already mapped to another currency")
	flag.Var(&sygnaIDFlags, "sygna-id", "Override legacy Sygna ID as KEY=sygna:... (repeatable)")
	fixedUUID := flag.String("uuid", "", "UUID for a new currency, or to choose among existing currencies with the same name")
	root := flag.String("root", "..", "Repository root")
	dryRun := flag.Bool("dry-run", false, "Print the planned changes without writing")
	flag.Parse()

	if *symbol == "" || *name == "" || len(platformFlags) == 0 {
		fmt.Println("Usage: go run . -symbol SYMBOL -name NAME -platform KEY:ADDRESS [-platform KEY:ADDRESS ...] [--coingecko-id ID] [-dry-run]")
		fmt.Println("\nExamples:")
		fmt.Println(`go run . -symbol NIGHT -name Midnight --coingecko-id midnight-3 -platform binance-smart-chain:0xfe930c2d63aed9b82fc4dbc801920dd2c1a3224f -platform cardano:0691b2fecca1ac4f53cb6dfb00b7013e561d1f34403b957cbb5af1fa4e49474854`)
		fmt.Println("go run . -symbol USDT -name Tether -platform polygon --token-address 0xc2132d05d31c914a87c6611c10748aeb04b58e8f")
		os.Exit(1)
	}

	normSymbol := strings.ToUpper(strings.TrimSpace(*symbol))
	if !symbolRe.MatchString(normSymbol) || strings.Contains(normSymbol, "..") {
		log.Fatalf("Error: invalid -symbol %q: only A-Z, 0-9, '$', '.', '_' and '-' are allowed", *symbol)
	}
	overrides := map[string]string{}
	for _, s := range sygnaIDFlags {
		k, v, ok := strings.Cut(s, "=")
		if !ok || !strings.HasPrefix(v, "sygna:") {
			log.Fatalf("Error: invalid -sygna-id %q, expected KEY=sygna:...", s)
		}
		overrides[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
	}

	a := &adder{
		root:          *root,
		symbol:        normSymbol,
		name:          strings.TrimSpace(*name),
		coingeckoID:   strings.ToLower(strings.TrimSpace(*coingeckoID)),
		uuid:          strings.TrimSpace(*fixedUUID),
		overrides:     overrides,
		allowSharedCG: *allowSharedCG,
		files:         map[string][]byte{},
	}
	for _, p := range platformFlags {
		key, addr, hasAddr := strings.Cut(p, ":")
		if !hasAddr {
			if len(platformFlags) > 1 && *tokenAddress != "" {
				log.Fatalf("Error: --token-address is ambiguous with multiple -platform flags; use -platform KEY:ADDRESS")
			}
			addr = *tokenAddress
		}
		key = strings.ToLower(strings.TrimSpace(key))
		if !platformKeyRe.MatchString(key) {
			log.Fatalf("Error: invalid platform key %q", key)
		}
		a.rawPlatforms = append(a.rawPlatforms, [2]string{key, strings.TrimSpace(addr)})
	}

	if err := a.run(*dryRun); err != nil {
		log.Fatalf("Error: %v", err)
	}
}
