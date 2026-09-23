// language-support emits public technical support metadata without running tools.
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"safepatch/internal/domain/feature"
	"safepatch/languages"
	"safepatch/languages/golang"
	"time"
)

func main() {
	registry, err := languages.NewRegistry(golang.Config{GoTool: "go", Parallelism: 1})
	if err != nil {
		fmt.Fprintln(os.Stderr, "language support unavailable")
		os.Exit(1)
	}
	features, err := feature.NewBuiltinCatalog("1.0.0", time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC))
	if err != nil {
		fmt.Fprintln(os.Stderr, "feature catalog unavailable")
		os.Exit(1)
	}
	report, err := languages.SupportCatalog(registry, features)
	if err != nil {
		fmt.Fprintln(os.Stderr, "language support unavailable")
		os.Exit(1)
	}
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		os.Exit(1)
	}
}
