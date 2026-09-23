package main

import (
	"archive/zip"
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestReleaseMetadataRejectsInjectionAndEditionAsVersion(t *testing.T) {
	m := metadata{Version: "1.5.2", Channel: "stable", Commit: strings.Repeat("a", 40), BuiltAt: "2026-09-07T00:00:00Z"}
	if err := validate(m); err != nil {
		t.Fatal(err)
	}
	for _, channel := range []string{"stable", "beta", "nightly", "lts", "LTS"} {
		candidate := m
		candidate.Channel = channel
		if err := validate(candidate); err != nil {
			t.Fatal(channel, err)
		}
	}
	for _, channel := range []string{"", "enterprise", "beta -tags=enterprise", "../lts", "lts\n"} {
		candidate := m
		candidate.Channel = channel
		if validate(candidate) == nil {
			t.Fatal("invalid channel accepted", channel)
		}
	}
	for _, value := range []string{"Enterprise", "1.5.2 -X injected=value", "../1.5.2", "01.5.2", "1.5.2\n"} {
		bad := m
		bad.Version = value
		if validate(bad) == nil {
			t.Fatal(value)
		}
	}
	bad := m
	bad.Commit = "$(print-secret)"
	if validate(bad) == nil {
		t.Fatal("commit injection")
	}
	bad = m
	bad.BuiltAt = "2026-09-07T00:00:00Z -X injected=value"
	if validate(bad) == nil {
		t.Fatal("timestamp injection")
	}
	bad = m
	bad.BuiltAt = "1979-01-01T00:00:00Z"
	if validate(bad) == nil {
		t.Fatal("unrepresentable ZIP time")
	}
}

func TestBuildEnvironmentCannotSelectEditionSource(t *testing.T) {
	env := buildEnv([]string{"GOFLAGS=-tags=enterprise", "goos=other", "GOARCH=other", "CGO_ENABLED=1", "GOEXPERIMENT=bad", "GOWORK=/foreign/go.work", "PATH=trusted"}, "linux", "arm64")
	joined := strings.Join(env, "\n")
	for _, bad := range []string{"enterprise", "other", "foreign", "=bad", "CGO_ENABLED=1"} {
		if strings.Contains(joined, bad) {
			t.Fatal(joined)
		}
	}
	for _, want := range []string{"GOOS=linux", "GOARCH=arm64", "CGO_ENABLED=0", "GOWORK=off", "PATH=trusted"} {
		if !strings.Contains(joined, want) {
			t.Fatal(joined)
		}
	}
}

func TestArchiveAllowlistDeterminismAndNoOverwrite(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "windows"} {
		t.Run(goos, func(t *testing.T) {
			dir := t.TempDir()
			out := t.TempDir()
			names := append([]string{}, commands...)
			if goos == "windows" {
				for i := range names {
					names[i] += ".exe"
				}
			}
			names = append(names, "release.json")
			for _, name := range append(names, "private-license-key.pem") {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(name), 0600); err != nil {
					t.Fatal(err)
				}
			}
			stamp := time.Date(2026, 9, 7, 0, 0, 0, 0, time.UTC)
			a, b := filepath.Join(out, "a.zip"), filepath.Join(out, "b.zip")
			if err := archive(a, dir, goos, stamp); err != nil {
				t.Fatal(err)
			}
			if err := archive(b, dir, goos, stamp); err != nil {
				t.Fatal(err)
			}
			one, _ := os.ReadFile(a)
			two, _ := os.ReadFile(b)
			if !bytes.Equal(one, two) {
				t.Fatal("non-deterministic archive")
			}
			if archive(a, dir, goos, stamp) == nil {
				t.Fatal("overwrote existing release")
			}
			z, err := zip.OpenReader(a)
			if err != nil {
				t.Fatal(err)
			}
			defer z.Close()
			if len(z.File) != 7 {
				t.Fatal("archive allowlist changed")
			}
			for i, f := range z.File {
				if f.Name != names[i] || !f.Mode().IsRegular() {
					t.Fatal(f.Name, f.Mode())
				}
				if f.Name != "release.json" && f.Mode().Perm() != 0755 {
					t.Fatal("lost executable permission")
				}
			}
			if err := os.Remove(filepath.Join(dir, names[0])); err != nil {
				t.Fatal(err)
			}
			if archive(filepath.Join(out, "incomplete.zip"), dir, goos, stamp) == nil {
				t.Fatal("missing binary accepted")
			}
		})
	}
}
