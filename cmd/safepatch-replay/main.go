package main

import (
	"os"

	"safepatch/internal/application"
)

func main() {
	os.Exit(application.Run("safepatch-replay", os.Args[1:], os.Stdout, os.Stderr))
}
