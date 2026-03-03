package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"os"
	"strings"

	"github.com/google/uuid"
)

type Currency struct {
	ID        string                  `json:"id"`
	Symbol    string                  `json:"symbol"`
	Name      string                  `json:"name"`
	Platforms map[string]PlatformInfo `json:"platforms"`
}

type PlatformInfo struct {
	TokenAddress string `json:"token_address"`
}

type CoingeckoMappingV3 struct {
	CurrencyID  string `json:"currency_id"`
	CoingeckoID string `json:"coingecko_id"`
}

func main() {
	symbol := flag.String("symbol", "", "Currency symbol (required, e.g., BTC, USDT)")
	name := flag.String("name", "", "Currency name (required, e.g., Bitcoin, Tether)")
	platform := flag.String("platform", "", "Platform key (required, e.g., ethereum, bitcoin)")
	tokenAddress := flag.String("token-address", "", "Token address (optional, default: native)")
	coingeckoID := flag.String("coingecko-id", "", "CoinGecko ID (optional, check README.md for more details)")

	flag.Parse()

	if *symbol == "" || *name == "" || *platform == "" {
		fmt.Println("Usage: go run add_currency.go -symbol SYMBOL -name NAME -platform PLATFORM --token-address TOKEN_ADDRESS [--coingecko-id ID]")
		fmt.Println("\nExamples:")
		fmt.Println("go run add_currency.go -symbol BTC -name Bitcoin -platform bitcoin --token-address native --coingecko-id bitcoin")
		fmt.Println("go run add_currency.go -symbol USDT -name Tether -platform polygon --token-address 0xc2132d05d31c914a87c6611c10748aeb04b58e8f")
		os.Exit(1)
	}

	*symbol = strings.ToUpper(strings.TrimSpace(*symbol))
	*name = strings.TrimSpace(*name)
	*platform = strings.ToLower(strings.TrimSpace(*platform))
	*tokenAddress = strings.ToLower(strings.TrimSpace(*tokenAddress))
	if *tokenAddress == "" {
		*tokenAddress = "native"
	}
	*coingeckoID = strings.ToLower(strings.TrimSpace(*coingeckoID))

	log.Printf("Adding currency: %s (%s) on %s\n", *symbol, *name, *platform)

	if err := addCurrency(*symbol, *name, *platform, *tokenAddress, *coingeckoID); err != nil {
		log.Fatalf("Error: %v", err)
	}

	log.Println("✅ Successfully added currency!")
}

func addCurrency(symbol, name, platform, tokenAddress, coingeckoID string) error {
	filename := fmt.Sprintf("../cryptocurrency_v3/%s.json", symbol)

	var currencies []Currency
	existingData, err := os.ReadFile(filename)
	if err == nil {
		if err := json.Unmarshal(existingData, &currencies); err != nil {
			return fmt.Errorf("failed to parse existing file %s: %v", filename, err)
		}
		log.Println("Found existing file")
	} else {
		log.Printf("Creating new file: %s\n", filename)
		currencies = []Currency{}
	}

	var targetCurrency *Currency
	for i := range currencies {
		if currencies[i].Name == name {
			targetCurrency = &currencies[i]
			break
		}
	}

	// Scenario 1: Found existing currency with the same name → Add platform
	if targetCurrency != nil {
		log.Printf("Found existing currency %q (ID: %s)\n", name, targetCurrency.ID)

		// Check if platform already exists
		if _, exists := targetCurrency.Platforms[platform]; exists {
			return fmt.Errorf("⚠️ Platform %q already exists", platform)
		}

		targetCurrency.Platforms[platform] = PlatformInfo{
			TokenAddress: tokenAddress,
		}

		log.Printf("✓ Added platform %q with token address %q", platform, tokenAddress)
	} else {
		// Scenario 2: No existing currency with the same name → Create new currency
		newUUID := uuid.New().String()
		log.Printf("Creating new currency %q with UUID: %s\n", name, newUUID)

		newCurrency := Currency{
			ID:     newUUID,
			Symbol: symbol,
			Name:   name,
			Platforms: map[string]PlatformInfo{
				platform: {TokenAddress: tokenAddress},
			},
		}

		currencies = append(currencies, newCurrency)

		log.Printf("✅ Created new currency %q\n", name)

		if err := updateCoingeckoMapping(newUUID, coingeckoID); err != nil {
			log.Printf("⚠️ Warning: Failed to update coingecko mapping: %v", err)
		}
	}

	data, err := json.MarshalIndent(currencies, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal JSON: %v", err)
	}

	if err := os.WriteFile(filename, data, 0644); err != nil {
		return fmt.Errorf("failed to write file: %v", err)
	}

	log.Printf("✅ Saved to %s\n", filename)

	return nil
}

func updateCoingeckoMapping(currencyUUID, coingeckoID string) error {
	mappingFile := "../coingecko_id/mapping_v3.json"

	var mapping []CoingeckoMappingV3
	existingData, err := os.ReadFile(mappingFile)
	if err != nil {
		return fmt.Errorf("%q not found", mappingFile)
	}

	if err := json.Unmarshal(existingData, &mapping); err != nil {
		return fmt.Errorf("failed to parse mapping: %v", err)
	}

	mapping = append(mapping, CoingeckoMappingV3{
		CurrencyID:  currencyUUID,
		CoingeckoID: coingeckoID,
	})

	data, err := json.MarshalIndent(mapping, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal mapping: %v", err)
	}

	if err := os.WriteFile(mappingFile, data, 0644); err != nil {
		return fmt.Errorf("failed to write mapping: %v", err)
	}

	return nil
}
