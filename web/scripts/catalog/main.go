// Generates public presentation data from the Go product configuration. No
// database, billing provider, customer data or runtime entitlement is accessed.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"safepatch/internal/config"
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
	c, err := config.PublicPricing(config.PricingOptions{DownloadURL: os.Getenv("SAFEPATCH_DOWNLOAD_URL"), TrialURL: os.Getenv("SAFEPATCH_TRIAL_URL"), SalesURL: os.Getenv("SAFEPATCH_SALES_URL"), BusinessTrialDays: days})
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
