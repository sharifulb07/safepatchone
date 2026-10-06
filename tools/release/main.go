// Command release packages the six shared entrypoints without edition inputs.
package main

import (
	"archive/zip"
	"context"
	"crypto/sha256"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	distribution "safepatch/internal/domain/release"
	"strings"
	"time"
)

var commands = []string{"safepatch", "safepatch-api", "safepatch-worker", "safepatch-collector", "safepatch-mcp-gateway", "safepatch-replay"}
var targets = []string{"linux/amd64", "linux/arm64", "darwin/amd64", "darwin/arm64", "windows/amd64", "windows/arm64"}
var commitPattern = regexp.MustCompile(`^[a-f0-9]{40}$`)

type metadata struct {
	Version  string   `json:"version"`
	Channel  string   `json:"channel"`
	Commit   string   `json:"commit"`
	BuiltAt  string   `json:"built_at"`
	Commands []string `json:"commands"`
	Targets  []string `json:"targets"`
}

func validate(m metadata) error {
	stamp, err := time.Parse(time.RFC3339, m.BuiltAt)
	if !distribution.ValidVersion(m.Version) || len(m.Version) > 64 || !commitPattern.MatchString(m.Commit) || err != nil || stamp.Year() < 1980 || stamp.Year() > 2107 || stamp.Format(time.RFC3339) != m.BuiltAt {
		return fmt.Errorf("release requires numeric version, full lowercase commit SHA and RFC3339 timestamp (1980-2107)")
	}
	if _, err := distribution.ParseChannel(m.Channel); err != nil {
		return err
	}
	return nil
}

func main() {
	var m metadata
	var out string
	flag.StringVar(&m.Version, "version", "", "product version, e.g. 1.5.2")
	flag.StringVar(&m.Channel, "channel", "stable", "distribution channel: stable, beta, nightly, lts")
	flag.StringVar(&m.Commit, "commit", "", "source commit SHA")
	flag.StringVar(&m.BuiltAt, "timestamp", "", "source commit time in RFC3339")
	flag.StringVar(&out, "out", "dist", "new output directory; existing paths are refused")
	flag.Parse()
	if flag.NArg() != 0 {
		fmt.Fprintln(os.Stderr, "unexpected arguments")
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if err := build(ctx, m, out); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func build(ctx context.Context, m metadata, out string) error {
	if err := validate(m); err != nil {
		return err
	}
	channel, _ := distribution.ParseChannel(m.Channel)
	m.Channel = string(channel)
	if err := os.Mkdir(out, 0755); err != nil {
		return err
	}
	m.Commands = commands
	m.Targets = targets
	raw, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err = os.WriteFile(filepath.Join(out, "release.json"), raw, 0644); err != nil {
		return err
	}
	stamp, _ := time.Parse(time.RFC3339, m.BuiltAt)
	var sums strings.Builder
	fmt.Fprintf(&sums, "%x  release.json\n", sha256.Sum256(raw))
	for _, target := range targets {
		parts := strings.Split(target, "/")
		dir, err := os.MkdirTemp("", "safepatch-release-")
		if err != nil {
			return err
		}
		err = func() error {
			defer os.RemoveAll(dir) // Only this freshly created private staging directory.
			ldflags := "-s -w -X safepatch/internal/application.BuildVersion=" + m.Version + " -X safepatch/internal/application.BuildCommit=" + m.Commit + " -X safepatch/internal/application.BuildTime=" + m.BuiltAt + " -X safepatch/internal/application.BuildChannel=" + m.Channel
			args := []string{"build", "-mod=readonly", "-trimpath", "-buildvcs=false", "-ldflags", ldflags, "-o", dir + string(os.PathSeparator)}
			for _, name := range commands {
				args = append(args, "./cmd/"+name)
			}
			// Reuse the toolchain selected by go.mod, including Go's automatic
			// security update, instead of an older ambient PATH installation.
			goBinary := "go"
			if runtime.GOOS == "windows" {
				goBinary += ".exe"
			}
			cmd := exec.CommandContext(ctx, filepath.Join(runtime.GOROOT(), "bin", goBinary), args...)
			cmd.Env = buildEnv(os.Environ(), parts[0], parts[1])
			cmd.Stdout = os.Stdout
			cmd.Stderr = os.Stderr
			fmt.Println("Building", target)
			if err := cmd.Run(); err != nil {
				return err
			}
			if err := os.WriteFile(filepath.Join(dir, "release.json"), raw, 0644); err != nil {
				return err
			}
			name := "safepatch_" + m.Version + "_" + parts[0] + "_" + parts[1] + ".zip"
			if err := archive(filepath.Join(out, name), dir, parts[0], stamp); err != nil {
				return err
			}
			f, err := os.Open(filepath.Join(out, name))
			if err != nil {
				return err
			}
			defer f.Close()
			h := sha256.New()
			if _, err = io.Copy(h, f); err != nil {
				return err
			}
			fmt.Fprintf(&sums, "%x  %s\n", h.Sum(nil), name)
			return nil
		}()
		if err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(out, "checksums.txt"), []byte(sums.String()), 0644)
}

func buildEnv(base []string, goos, arch string) []string {
	var result []string
	for _, item := range base {
		key := strings.ToUpper(strings.SplitN(item, "=", 2)[0])
		switch key {
		case "GOOS", "GOARCH", "CGO_ENABLED", "GOFLAGS", "GOEXPERIMENT", "GOAMD64", "GOARM64", "GOWORK", "GOTOOLCHAIN":
			continue
		}
		result = append(result, item)
	}
	return append(result, "GOOS="+goos, "GOARCH="+arch, "CGO_ENABLED=0", "GOFLAGS=", "GOEXPERIMENT=", "GOAMD64=v1", "GOARM64=v8.0", "GOWORK=off", "GOTOOLCHAIN=local")
}

func archive(path, dir, goos string, stamp time.Time) (err error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0644)
	if err != nil {
		return err
	}
	defer func() {
		if e := f.Close(); err == nil {
			err = e
		}
	}()
	z := zip.NewWriter(f)
	defer func() {
		if e := z.Close(); err == nil {
			err = e
		}
	}()
	names := append([]string{}, commands...)
	if goos == "windows" {
		for i := range names {
			names[i] += ".exe"
		}
	}
	names = append(names, "release.json")
	for _, name := range names {
		info, e := os.Lstat(filepath.Join(dir, name))
		if e != nil {
			return e
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("non-regular release input: %s", name)
		}
		header := &zip.FileHeader{Name: name, Method: zip.Deflate}
		header.SetModTime(stamp.UTC())
		header.SetMode(0755)
		if name == "release.json" {
			header.SetMode(0644)
		}
		w, e := z.CreateHeader(header)
		if e != nil {
			return e
		}
		r, e := os.Open(filepath.Join(dir, name))
		if e != nil {
			return e
		}
		_, e = io.Copy(w, r)
		closeErr := r.Close()
		if e != nil {
			return e
		}
		if closeErr != nil {
			return closeErr
		}
	}
	return nil
}
