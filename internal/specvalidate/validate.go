// Package specvalidate provides validation for Liza specification documents.
// It validates both Vision and Delivery specs against their required sections,
// checks for acceptance criteria format, and identifies empty or incomplete sections.
package specvalidate

import (
	"fmt"
	"regexp"
	"strings"
)

// SpecType identifies the kind of specification being validated.
type SpecType string

const (
	SpecTypeVision   SpecType = "vision"
	SpecTypeDelivery SpecType = "delivery"
)

// ValidationResult contains the outcome of spec validation.
type ValidationResult struct {
	Valid    bool
	Missing  []string
	Warnings []string
}

// ValidateSpecFile reads and validates a spec file at the given path.
func ValidateSpecFile(content string, specType SpecType) *ValidationResult {
	result := &ValidationResult{Valid: true}

	sections := parseSections(content)
	required := requiredSections(specType)

	for _, req := range required {
		found := false
		for _, section := range sections {
			if sectionMatches(section.Name, req) {
				found = true
				if strings.TrimSpace(section.Content) == "" {
					result.Warnings = append(result.Warnings, fmt.Sprintf("section %q is empty", req))
				}
				break
			}
		}
		if !found {
			result.Missing = append(result.Missing, req)
			result.Valid = false
		}
	}

	// For delivery specs, check acceptance criteria format and deeper validation
	if specType == SpecTypeDelivery {
		checkAcceptanceCriteria(sections, result)
		checkRequirementIDs(sections, result)
		checkACRequirementLinkage(sections, result)
		checkVerificationCommands(sections, result)
		checkOpenQuestions(sections, result)
	}

	return result
}

// checkAcceptanceCriteria validates that the Acceptance Criteria section
// contains at least one Given/When/Then block.
func checkAcceptanceCriteria(sections []Section, result *ValidationResult) {
	for _, section := range sections {
		if sectionMatches(section.Name, "Acceptance Criteria") {
			if !hasGivenWhenThen(section.Content) {
				result.Warnings = append(result.Warnings,
					"Acceptance Criteria section has no Given/When/Then blocks")
			}
			return
		}
	}
}

// hasGivenWhenThen checks if content contains at least one Given/When/Then pattern.
func hasGivenWhenThen(content string) bool {
	lower := strings.ToLower(content)
	return strings.Contains(lower, "given:") &&
		strings.Contains(lower, "when:") &&
		strings.Contains(lower, "then:")
}

// sectionMatches checks if a section name matches a required section name.
// Comparison is case-insensitive and ignores leading/trailing whitespace.
func sectionMatches(actual, expected string) bool {
	return strings.EqualFold(strings.TrimSpace(actual), strings.TrimSpace(expected))
}

// requirementIDPattern matches R1, R2, R-1, R-02, etc.
var requirementIDPattern = regexp.MustCompile(`(?i)\bR-?\d+\b`)

// acLinkagePattern matches AC-1 (R1), AC-02 (R-1, R-2), etc.
var acLinkagePattern = regexp.MustCompile(`(?i)\bAC-?\d+\s*\(R`)

// checkRequirementIDs warns if a Requirements section exists but has no R# IDs.
func checkRequirementIDs(sections []Section, result *ValidationResult) {
	for _, section := range sections {
		if sectionMatches(section.Name, "Requirements") || sectionMatches(section.Name, "Requirement References") {
			if !requirementIDPattern.MatchString(section.Content) {
				result.Warnings = append(result.Warnings,
					"Requirements section has no requirement IDs (expected R1, R2, ...)")
			}
			return
		}
	}
	// Requirements section is optional for backward compatibility
	result.Warnings = append(result.Warnings,
		"no Requirements section found (recommended for traceability)")
}

// checkACRequirementLinkage warns if Acceptance Criteria contains AC IDs but
// none of them reference a requirement ID (e.g. AC-1 (R1)).
func checkACRequirementLinkage(sections []Section, result *ValidationResult) {
	for _, section := range sections {
		if sectionMatches(section.Name, "Acceptance Criteria") {
			// Only check linkage if there are AC-style identifiers
			if regexp.MustCompile(`(?i)\bAC-?\d+`).MatchString(section.Content) {
				if !acLinkagePattern.MatchString(section.Content) {
					result.Warnings = append(result.Warnings,
						"Acceptance Criteria has AC IDs but no requirement linkage (expected AC-1 (R1) format)")
				}
			}
			return
		}
	}
}

// checkVerificationCommands warns if the Verification Plan section doesn't
// contain any command-like content (code blocks, shell commands, etc.).
func checkVerificationCommands(sections []Section, result *ValidationResult) {
	for _, section := range sections {
		if sectionMatches(section.Name, "Verification Plan") || sectionMatches(section.Name, "Verification Commands") {
			content := section.Content
			hasCodeBlock := strings.Contains(content, "```")
			hasShellCmd := strings.Contains(content, "go test") ||
				strings.Contains(content, "npm test") ||
				strings.Contains(content, "pytest") ||
				strings.Contains(content, "make test") ||
				strings.Contains(content, "verify_commands")
			if !hasCodeBlock && !hasShellCmd {
				result.Warnings = append(result.Warnings,
					"Verification Plan has no executable commands or code blocks (expected verify_commands)")
			}
			return
		}
	}
}

// checkOpenQuestions warns if the Open Questions section contains blocking items.
// A blocking open question contains "block" (case-insensitive) in its text.
func checkOpenQuestions(sections []Section, result *ValidationResult) {
	for _, section := range sections {
		if sectionMatches(section.Name, "Open Questions") {
			lower := strings.ToLower(section.Content)
			if strings.Contains(lower, "block") && !strings.Contains(lower, "none") {
				result.Warnings = append(result.Warnings,
					"Open Questions section may contain blocking items — resolve before IMPLEMENTING")
			}
			return
		}
	}
}

