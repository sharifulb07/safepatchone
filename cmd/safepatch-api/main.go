package main

import (
	"os"

	"safepatch/internal/application"
	"safepatch/internal/platform/onboardinghost"
)

func main() {
	if len(os.Args) == 2 && os.Args[1] == "serve" {
		os.Exit(onboardinghost.Run(os.Stderr))
	}
	os.Exit(application.Run("safepatch-api", os.Args[1:], os.Stdout, os.Stderr))
}
