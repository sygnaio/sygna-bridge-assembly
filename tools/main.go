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

	var setAddressFlags, removeFlags repeated
	update := flag.Bool("update", false, "Change an existing currency (found by -symbol, -name and optional -uuid) instead of adding")
	flag.Var(&setAddressFlags, "set-address", "With -update: new token address as KEY:ADDRESS (repeatable)")
	setName := flag.String("set-name", "", "With -update: new currency name")
	setSymbol := flag.String("set-symbol", "", "With -update: new currency symbol")
	setCoingeckoID := flag.String("set-coingecko-id", "", "With -update: new CoinGecko ID")
	flag.Var(&removeFlags, "remove-platform", "With -update: platform key to remove (repeatable)")
	deactivate := flag.Bool("deactivate", false, "With -update: delist the currency (all legacy files become inactive)")
	deleteV3 := flag.Bool("delete-v3", false, "With -deactivate: also remove the currency from cryptocurrency_v3")
	flag.Parse()

	overrides := map[string]string{}
	for _, s := range sygnaIDFlags {
		k, v, ok := strings.Cut(s, "=")
		if !ok || !strings.HasPrefix(v, "sygna:") {
			log.Fatalf("Error: invalid -sygna-id %q, expected KEY=sygna:...", s)
		}
		overrides[strings.ToLower(strings.TrimSpace(k))] = strings.TrimSpace(v)
	}

	updateOnly := len(setAddressFlags) > 0 || len(removeFlags) > 0 || *setName != "" || *setSymbol != "" || *setCoingeckoID != "" || *deactivate || *deleteV3
	if *update {
		if *symbol == "" || *name == "" {
			fmt.Println("Usage: go run . -update -symbol SYMBOL -name NAME [-uuid UUID] CHANGE... [-dry-run]")
			fmt.Println("\nChanges: -set-address KEY:ADDRESS, -set-name NAME, -set-symbol SYMBOL, -set-coingecko-id ID, -remove-platform KEY, -deactivate [-delete-v3]")
			fmt.Println("\nExample:")
			fmt.Println("go run . -update -symbol XAUT -name 'Tether Gold' -set-address ethereum:0x68749665FF8D2d112Fa859AA293F07A622782F38 -dry-run")
			os.Exit(1)
		}
		if len(platformFlags) > 0 || *tokenAddress != "" || *coingeckoID != "" {
			log.Fatalf("Error: -platform, --token-address and --coingecko-id are for adding; with -update use -set-address, -remove-platform or -set-coingecko-id")
		}
		u := &updater{
			repo:           newRepo(*root),
			symbol:         strings.ToUpper(strings.TrimSpace(*symbol)),
			name:           strings.TrimSpace(*name),
			uuid:           strings.TrimSpace(*fixedUUID),
			overrides:      overrides,
			allowSharedCG:  *allowSharedCG,
			setName:        strings.TrimSpace(*setName),
			setSymbol:      strings.ToUpper(strings.TrimSpace(*setSymbol)),
			setCoingeckoID: strings.ToLower(strings.TrimSpace(*setCoingeckoID)),
			deactivate:     *deactivate,
			deleteV3:       *deleteV3,
		}
		for _, p := range setAddressFlags {
			key, addr, ok := strings.Cut(p, ":")
			key = strings.ToLower(strings.TrimSpace(key))
			if !ok || !platformKeyRe.MatchString(key) {
				log.Fatalf("Error: invalid -set-address %q, expected KEY:ADDRESS", p)
			}
			u.setAddress = append(u.setAddress, [2]string{key, strings.TrimSpace(addr)})
		}
		for _, key := range removeFlags {
			key = strings.ToLower(strings.TrimSpace(key))
			if !platformKeyRe.MatchString(key) {
				log.Fatalf("Error: invalid -remove-platform %q", key)
			}
			u.removePlatforms = append(u.removePlatforms, key)
		}
		if err := u.run(*dryRun); err != nil {
			log.Fatalf("Error: %v", err)
		}
		return
	}
	if updateOnly {
		log.Fatalf("Error: -set-*, -remove-platform, -deactivate and -delete-v3 need -update")
	}

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
	a := &adder{
		repo:          newRepo(*root),
		symbol:        normSymbol,
		name:          strings.TrimSpace(*name),
		coingeckoID:   strings.ToLower(strings.TrimSpace(*coingeckoID)),
		uuid:          strings.TrimSpace(*fixedUUID),
		overrides:     overrides,
		allowSharedCG: *allowSharedCG,
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
