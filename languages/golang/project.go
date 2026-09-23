// Package golang implements Go verification through the shared SDK and Runner.
// Commercial entitlement and actor permission decisions belong to application.
package golang

import (
	"errors"
	"path"
	"slices"
	"strings"

	"golang.org/x/mod/modfile"
	"safepatch/internal/evidence"
	"safepatch/internal/verification"
)

var ErrProject = errors.New("invalid or incomplete Go module manifest")

type project struct {
	modules    []string
	workspaces map[string][]string
}

func discover(target verification.Target) (project, error) {
	p := project{workspaces: map[string][]string{}}
	if target.Validate() != nil {
		return p, ErrProject
	}
	files := map[string]bool{}
	for _, name := range target.Files {
		files[name] = true
		if path.Base(name) == "go.mod" && !excluded(name) {
			p.modules = append(p.modules, path.Dir(name))
		}
	}
	slices.Sort(p.modules)
	if len(p.modules) > verification.MaxSteps {
		return p, ErrProject
	}
	local := func(base, reference string) (string, error) {
		if reference == "" || strings.ContainsAny(reference, "\\:\x00") || strings.HasPrefix(reference, "/") {
			return "", ErrProject
		}
		resolved := path.Clean(path.Join(base, reference))
		if resolved != "." && !evidence.ValidPath(resolved) {
			return "", ErrProject
		}
		if !files[path.Join(resolved, "go.mod")] {
			return "", ErrProject
		}
		return resolved, nil
	}
	for _, dir := range p.modules {
		name := path.Join(dir, "go.mod")
		data, ok := target.Metadata[name]
		if !ok {
			return p, ErrProject
		}
		module, err := modfile.Parse(name, []byte(data), nil)
		if err != nil || module.Module == nil {
			return p, ErrProject
		}
		for _, replace := range module.Replace {
			if replace.New.Version == "" {
				if _, err := local(dir, replace.New.Path); err != nil {
					return p, err
				}
			}
		}
	}
	for _, name := range target.Files {
		if path.Base(name) != "go.work" || excluded(name) {
			continue
		}
		data, ok := target.Metadata[name]
		if !ok {
			return p, ErrProject
		}
		work, err := modfile.ParseWork(name, []byte(data), nil)
		if err != nil || len(work.Use) == 0 {
			return p, ErrProject
		}
		base := path.Dir(name)
		seen := map[string]bool{}
		for _, use := range work.Use {
			dir, err := local(base, use.Path)
			if err != nil || seen[dir] || !slices.Contains(p.modules, dir) {
				return p, ErrProject
			}
			seen[dir] = true
			p.workspaces[base] = append(p.workspaces[base], dir)
		}
		for _, replace := range work.Replace {
			if replace.New.Version == "" {
				if _, err := local(base, replace.New.Path); err != nil {
					return p, err
				}
			}
		}
	}
	// Go automatically uses the nearest ancestor workspace. A module omitted
	// from that workspace cannot silently disappear from verification coverage.
	for _, dir := range p.modules {
		for parent := dir; ; parent = path.Dir(parent) {
			if members, ok := p.workspaces[parent]; ok {
				if !slices.Contains(members, dir) {
					return p, ErrProject
				}
				break
			}
			if parent == "." {
				break
			}
		}
	}
	return p, nil
}
func excluded(name string) bool {
	for _, part := range strings.Split(name, "/") {
		if part == "vendor" || part == ".git" || part == "node_modules" {
			return true
		}
	}
	return false
}
