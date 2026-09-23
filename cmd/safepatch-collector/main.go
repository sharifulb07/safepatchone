package main

import (
	"os"

	"safepatch/internal/application"
)

func main() {
	os.Exit(application.Run("safepatch-collector", os.Args[1:], os.Stdout, os.Stderr))
}
