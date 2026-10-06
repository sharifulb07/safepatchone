// Generates public presentation data from the Go product configuration. No
// database, billing provider, customer data or runtime entitlement is accessed.
package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"safepatch/internal/config"
	"safepatch/internal/domain/edition"
	"safepatch/internal/domain/jsonobject"
	"strconv"
)

func main() {
	days := 0
	if raw := os.Getenv("SAFEPATCH_BUSINESS_TRIAL_DAYS"); raw != "" {
		var err error
		days, err = strconv.Atoi(raw)
		if err != nil {
			fail()
		}
	}
	var prices map[edition.Key]config.PublishedPrice
	if raw := os.Getenv("SAFEPATCH_PUBLIC_PRICES"); raw != "" {
		checked, err := jsonobject.Normalize([]byte(raw), 4096)
		if err != nil {
			fail()
		}
		decoder := json.NewDecoder(bytes.NewReader(checked))
		decoder.DisallowUnknownFields()
		if decoder.Decode(&prices) != nil {
			fail()
		}
	}
	c, err := config.PublicPricing(config.PricingOptions{DownloadURL: os.Getenv("SAFEPATCH_DOWNLOAD_URL"), TrialURL: os.Getenv("SAFEPATCH_TRIAL_URL"), SalesURL: os.Getenv("SAFEPATCH_SALES_URL"), BusinessTrialDays: days, Prices: prices})
	if err != nil {
		fail()
	}
	if json.NewEncoder(os.Stdout).Encode(c) != nil {
		fail()
	}
}
func fail() {
	fmt.Fprintln(os.Stderr, "public product configuration is invalid or cannot be written")
	os.Exit(1)
}
