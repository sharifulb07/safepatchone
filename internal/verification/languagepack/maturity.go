package languagepack

import "slices"

// Maturity is language support depth, not a proof level, entitlement or PASS.
type Maturity string

const (
	PV0 Maturity = "PV0"
	PV1 Maturity = "PV1"
	PV2 Maturity = "PV2"
	PV3 Maturity = "PV3"
	PV4 Maturity = "PV4"
	PV5 Maturity = "PV5"
	PV6 Maturity = "PV6"
	PV7 Maturity = "PV7"
	PV8 Maturity = "PV8"
)

func Maturities() []Maturity   { return []Maturity{PV0, PV1, PV2, PV3, PV4, PV5, PV6, PV7, PV8} }
func (m Maturity) Rank() int   { return slices.Index(Maturities(), m) }
func (m Maturity) Valid() bool { return m.Rank() >= 0 }

type MaturityStage struct {
	Level       Maturity `json:"level"`
	Requirement string   `json:"requirement"`
}

func MaturityRubric() []MaturityStage {
	return []MaturityStage{
		{PV0, "Roadmap only; no installed language pack."},
		{PV1, "Bounded source/manifest detection; no parsing or verification claim."},
		{PV2, "Normalization of supplied concepts into shared SIR; source extraction may be unavailable."},
		{PV3, "Registered verification execution through the shared SDK/Runner; depth depends on individual capabilities."},
		{PV4, "Validated source extraction and representative build/test integration coverage."},
		{PV5, "Validated security and compatibility verification across supported toolchains."},
		{PV6, "Validated mutation/fuzz/performance coverage and resource isolation."},
		{PV7, "Validated Replay/runtime correlation with source and proof evidence."},
		{PV8, "Validated continuous and composed proof lifecycle across supported integrations."},
	}
}

// Current SDK contracts substantiate at most PV3. Merely adding a verifier must
// not promote a pack to higher levels without extending conformance evidence.
func supportedMaturity(caps []Capability) Maturity {
	if !slices.Contains(caps, Detect) {
		return PV0
	}
	if !slices.Contains(caps, NormalizeSIR) {
		return PV1
	}
	if !slices.Contains(caps, Verify) {
		return PV2
	}
	return PV3
}

func (d Descriptor) Limitations() []string {
	result := []string{"Detection is a metadata hint, not syntax validation or PASS.", "SIR normalization consumes supplied concepts; no source AST extractor is installed by this pack."}
	if !slices.Contains(d.Capabilities, Verify) {
		result = append(result, "Verification execution is unavailable.")
	} else {
		result = append(result, "Execution requires trusted tool configuration and a provisioned Runner; optional tools may be unavailable.")
	}
	return result
}
