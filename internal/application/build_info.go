package application

// Linker metadata identifies software only; it never supplies runtime grants.
var BuildVersion = "dev"
var BuildCommit = "unknown"
var BuildTime = "unknown"
var BuildChannel = "stable"

type buildInfo struct {
	Version string `json:"version"`
	Commit  string `json:"commit"`
	BuiltAt string `json:"built_at"`
}
