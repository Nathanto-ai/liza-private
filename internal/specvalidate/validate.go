// Package specvalidate provides validation for Liza specification documents.
// It validates both Vision and Delivery specs against their required sections,
// checks for acceptance criteria format, and identifies empty or incomplete sections.
package specvalidate

import (
	"fmt"
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

	// For delivery specs, check acceptance criteria format
	if specType == SpecTypeDelivery {
		checkAcceptanceCriteria(sections, result)
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
