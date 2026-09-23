// Package languagepack defines the shared LanguagePack SDK. Technical support
// and commercial admission are separate; no edition metadata enters this API.
package languagepack

import (
	"context"
	"errors"
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"

	"safepatch/internal/evidence"
	"safepatch/internal/sir"
	"safepatch/internal/verification"
)

type Language string

const (
	Go         Language = "go"
	JavaScript Language = "javascript"
	TypeScript Language = "typescript"
	Python     Language = "python"
	Java       Language = "java"
	Rust       Language = "rust"
	CSharp     Language = "csharp"
	Kotlin     Language = "kotlin"
	Ruby       Language = "ruby"
	PHP        Language = "php"
)

type Capability string

const (
	Detect       Capability = "language.detect"
	NormalizeSIR Capability = "language.sir.normalize"
	Verify       Capability = "language.verify"
	MaxPacks                = 128
)

var (
	ErrInvalidPack  = errors.New("invalid language pack")
	ErrPackNotFound = errors.New("language pack not found")
	ErrUnavailable  = errors.New("language pack capability unavailable")
	ErrPackFailed   = errors.New("language pack operation failed")
	token           = regexp.MustCompile(`^[a-z][a-z0-9_-]{0,63}$`)
	version         = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?(\+[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$`)
)

type Descriptor struct {
	ID           string       `json:"id"`
	Version      string       `json:"version"`
	Language     Language     `json:"language"`
	Capabilities []Capability `json:"capabilities"`
	Maturity     Maturity     `json:"maturity"`
}
type Detection struct {
	State verification.DetectionState `json:"state"`
	Files []string                    `json:"files"`
}

// LanguagePack implementations are trusted Go extensions, not repository-loaded
// plugins. Normalize consumes extracted concepts; it does not imply AST extraction.
// Verifiers must execute through the existing SDK and secure Runner.
type LanguagePack interface {
	Descriptor() Descriptor
	Detect(context.Context, verification.Target) (Detection, error)
	Normalize(context.Context, sir.Extraction) (sir.Representation, error)
	Verifiers() []verification.Verifier
}

// Definition is immutable adapter composition after New. Marker detection is a
// language hint, not dependency resolution, code parsing or verification success.
type Definition struct {
	ID               string
	Version          string
	Language         Language
	Extensions       []string
	Markers          []string
	AmbiguousMarkers []string
	Mappings         []sir.Mapping
	Verifiers        []verification.Verifier
}
type Pack struct {
	descriptor                     Descriptor
	extensions, markers, ambiguous []string
	normalizer                     *sir.Normalizer
	verifiers                      []verification.Verifier
}

func New(d Definition) (*Pack, error) {
	if !evidence.ValidIdentifier(d.ID) || len(d.Version) > 128 || !version.MatchString(d.Version) || !validLanguage(d.Language) || len(d.Extensions) == 0 || len(d.Extensions) > 16 || len(d.Markers) > 16 || len(d.AmbiguousMarkers) > 16 || len(d.Verifiers) > 64 || len(d.Mappings) == 0 || len(d.Mappings) > sir.MaxMappings {
		return nil, ErrInvalidPack
	}
	seen := map[string]bool{}
	for _, ext := range d.Extensions {
		if len(ext) < 2 || len(ext) > 16 || ext[0] != '.' || !token.MatchString(ext[1:]) || seen[ext] {
			return nil, ErrInvalidPack
		}
		seen[ext] = true
	}
	seen = map[string]bool{}
	for _, group := range [][]string{d.Markers, d.AmbiguousMarkers} {
		for _, marker := range group {
			if !evidence.ValidPath(marker) || path.Base(marker) != marker || len(marker) > 128 || seen[marker] {
				return nil, ErrInvalidPack
			}
			seen[marker] = true
		}
	}
	for _, m := range d.Mappings {
		if m.Language != string(d.Language) {
			return nil, ErrInvalidPack
		}
	}
	n, err := sir.NewNormalizer(d.Mappings)
	if err != nil {
		return nil, err
	}
	if _, err := verification.NewRegistry(d.Verifiers...); err != nil {
		return nil, err
	}
	caps := []Capability{Detect, NormalizeSIR}
	if len(d.Verifiers) > 0 {
		caps = append(caps, Verify)
	}
	return &Pack{descriptor: Descriptor{ID: d.ID, Version: d.Version, Language: d.Language, Capabilities: caps, Maturity: supportedMaturity(caps)}, extensions: slices.Clone(d.Extensions), markers: slices.Clone(d.Markers), ambiguous: slices.Clone(d.AmbiguousMarkers), normalizer: n, verifiers: slices.Clone(d.Verifiers)}, nil
}
func (p *Pack) Descriptor() Descriptor {
	if p == nil {
		return Descriptor{}
	}
	d := p.descriptor
	d.Capabilities = slices.Clone(d.Capabilities)
	return d
}
func (p *Pack) Verifiers() []verification.Verifier {
	if p == nil {
		return nil
	}
	return slices.Clone(p.verifiers)
}
func (p *Pack) Detect(ctx context.Context, target verification.Target) (Detection, error) {
	failure := Detection{State: verification.DetectionUnknown, Files: []string{}}
	if err := check(ctx); err != nil {
		return failure, err
	}
	if p == nil || target.Validate() != nil {
		return failure, ErrInvalidPack
	}
	result := Detection{State: verification.NotApplicable, Files: []string{}}
	ambiguous := false
	for _, file := range target.Files {
		if err := ctx.Err(); err != nil {
			return failure, err
		}
		if slices.Contains(p.extensions, path.Ext(file)) || slices.Contains(p.markers, path.Base(file)) {
			result.Files = append(result.Files, file)
		}
		if slices.Contains(p.ambiguous, path.Base(file)) {
			ambiguous = true
		}
	}
	slices.Sort(result.Files)
	if len(result.Files) > 0 {
		result.State = verification.Detected
	} else if ambiguous {
		result.State = verification.DetectionUnknown
	}
	return result, nil
}
func (p *Pack) Normalize(ctx context.Context, in sir.Extraction) (sir.Representation, error) {
	if err := check(ctx); err != nil {
		return sir.Representation{}, err
	}
	if p == nil || p.normalizer == nil || len(in.Concepts) > sir.MaxNodes {
		return sir.Representation{}, ErrInvalidPack
	}
	for _, c := range in.Concepts {
		if c.Language != string(p.descriptor.Language) {
			return sir.Representation{}, ErrInvalidPack
		}
	}
	return p.normalizer.Normalize(ctx, in)
}

type entry struct {
	descriptor Descriptor
	pack       LanguagePack
	verifiers  *verification.Registry
}
type Registry struct{ entries map[string]entry }

// NewRegistry freezes support discovery. Duplicate IDs/languages cannot replace
// installed code. Custom packs must honor context cancellation and be concurrency safe.
func NewRegistry(packs ...LanguagePack) (result *Registry, err error) {
	defer func() {
		if recover() != nil {
			result = nil
			err = ErrInvalidPack
		}
	}()
	if len(packs) == 0 || len(packs) > MaxPacks {
		return nil, ErrInvalidPack
	}
	r := &Registry{entries: map[string]entry{}}
	languages := map[Language]bool{}
	for _, pack := range packs {
		if pack == nil {
			return nil, ErrInvalidPack
		}
		d := pack.Descriptor()
		if !evidence.ValidIdentifier(d.ID) || !validLanguage(d.Language) || len(d.Version) > 128 || !version.MatchString(d.Version) || len(d.Capabilities) == 0 || len(d.Capabilities) > 3 {
			return nil, ErrInvalidPack
		}
		d.Capabilities = slices.Clone(d.Capabilities)
		slices.Sort(d.Capabilities)
		if d.Maturity != supportedMaturity(d.Capabilities) || !slices.Contains(d.Capabilities, Detect) {
			return nil, ErrInvalidPack
		}
		for i, c := range d.Capabilities {
			if !slices.Contains([]Capability{Detect, NormalizeSIR, Verify}, c) || i > 0 && d.Capabilities[i-1] == c {
				return nil, ErrInvalidPack
			}
		}
		if _, exists := r.entries[d.ID]; exists || languages[d.Language] {
			return nil, ErrInvalidPack
		}
		v := pack.Verifiers()
		if len(v) > 64 || slices.Contains(d.Capabilities, Verify) != (len(v) > 0) {
			return nil, ErrInvalidPack
		}
		vr, err := verification.NewRegistry(v...)
		if err != nil {
			return nil, err
		}
		r.entries[d.ID] = entry{d, pack, vr}
		languages[d.Language] = true
	}
	return r, nil
}
func (r *Registry) Discover() []Descriptor {
	result := []Descriptor{}
	if r == nil {
		return result
	}
	for _, e := range r.entries {
		d := e.descriptor
		d.Capabilities = slices.Clone(d.Capabilities)
		result = append(result, d)
	}
	slices.SortFunc(result, func(a, b Descriptor) int { return strings.Compare(a.ID, b.ID) })
	return result
}
func (r *Registry) get(id string, capability Capability) (entry, error) {
	if r == nil {
		return entry{}, ErrInvalidPack
	}
	e, ok := r.entries[id]
	if !ok {
		return entry{}, ErrPackNotFound
	}
	if !slices.Contains(e.descriptor.Capabilities, capability) {
		return entry{}, ErrUnavailable
	}
	return e, nil
}
func (r *Registry) VerifierRegistry(id string) (*verification.Registry, error) {
	e, err := r.get(id, Verify)
	return e.verifiers, err
}

// Registry calls validate returned facts and isolate mutable input/output DTOs.
// Extension errors are redacted while errors.Is still exposes their cause.
type operationError struct{ cause error }

func (*operationError) Error() string     { return "language pack operation failed" }
func (e *operationError) Unwrap() []error { return []error{ErrPackFailed, e.cause} }
func (r *Registry) Detect(ctx context.Context, id string, target verification.Target) (result Detection, err error) {
	fail := func(cause error) (Detection, error) {
		return Detection{State: verification.DetectionUnknown, Files: []string{}}, &operationError{cause}
	}
	defer func() {
		if recover() != nil {
			result, err = fail(ErrInvalidPack)
		}
	}()
	if err := check(ctx); err != nil {
		return fail(err)
	}
	if target.Validate() != nil {
		return fail(ErrInvalidPack)
	}
	e, err := r.get(id, Detect)
	if err != nil {
		return fail(err)
	}
	owned := target
	owned.Files = slices.Clone(target.Files)
	owned.Metadata = maps.Clone(target.Metadata)
	result, err = e.pack.Detect(ctx, owned)
	if ctx.Err() != nil {
		return fail(ctx.Err())
	}
	if err != nil {
		return fail(err)
	}
	if len(result.Files) > len(target.Files) {
		return fail(ErrInvalidPack)
	}
	known := map[string]bool{}
	for _, name := range target.Files {
		known[name] = true
	}
	result.Files = append([]string{}, result.Files...)
	slices.Sort(result.Files)
	for i, name := range result.Files {
		if !known[name] || i > 0 && result.Files[i-1] == name {
			return fail(ErrInvalidPack)
		}
	}
	switch result.State {
	case verification.Detected:
		if len(result.Files) == 0 {
			return fail(ErrInvalidPack)
		}
	case verification.NotApplicable, verification.DetectionUnknown, verification.DetectionUnavailable:
		if len(result.Files) != 0 {
			return fail(ErrInvalidPack)
		}
	default:
		return fail(ErrInvalidPack)
	}
	return result, nil
}
func (r *Registry) Normalize(ctx context.Context, id string, in sir.Extraction) (result sir.Representation, err error) {
	fail := func(cause error) (sir.Representation, error) { return sir.Representation{}, &operationError{cause} }
	defer func() {
		if recover() != nil {
			result, err = fail(ErrInvalidPack)
		}
	}()
	if err := check(ctx); err != nil {
		return fail(err)
	}
	e, err := r.get(id, NormalizeSIR)
	if err != nil {
		return fail(err)
	}
	if len(in.Files) > sir.MaxFiles || len(in.Concepts) > sir.MaxNodes {
		return fail(ErrInvalidPack)
	}
	for _, c := range in.Concepts {
		if c.Language != string(e.descriptor.Language) {
			return fail(ErrInvalidPack)
		}
	}
	owned := in
	owned.Files = slices.Clone(in.Files)
	owned.Concepts = slices.Clone(in.Concepts)
	result, err = e.pack.Normalize(ctx, owned)
	if ctx.Err() != nil {
		return fail(ctx.Err())
	}
	if err != nil {
		return fail(err)
	}
	f, err := result.Fields()
	if err != nil || f.Metadata != in.Metadata || len(f.Nodes) != len(in.Concepts) || len(f.Files) != len(in.Files) {
		return fail(ErrInvalidPack)
	}
	files := map[string]string{}
	for _, file := range in.Files {
		if _, ok := files[file.Path]; ok {
			return fail(ErrInvalidPack)
		}
		files[file.Path] = file.ContentHash
	}
	for _, file := range f.Files {
		if files[file.Path] != file.ContentHash {
			return fail(ErrInvalidPack)
		}
	}
	concepts := map[string]sir.Concept{}
	for _, c := range in.Concepts {
		if _, ok := concepts[c.ID]; ok {
			return fail(ErrInvalidPack)
		}
		concepts[c.ID] = c
	}
	for _, node := range f.Nodes {
		if c, ok := concepts[node.ID]; !ok || c != node.Concept {
			return fail(ErrInvalidPack)
		}
	}
	return result, nil
}
func check(ctx context.Context) error {
	if ctx == nil {
		return ErrInvalidPack
	}
	return ctx.Err()
}
func validLanguage(language Language) bool {
	return len(language) <= 64 && token.MatchString(string(language))
}
