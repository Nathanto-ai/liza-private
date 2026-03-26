package specvalidate

// requiredSections returns the section names required for the given spec type.
func requiredSections(specType SpecType) []string {
	switch specType {
	case SpecTypeVision:
		return []string{
			"Problem Statement",
			"Target Users",
			"MVP Scope",
			"Explicit Out of Scope",
			"Success Criteria",
			"Risks and Assumptions",
		}
	case SpecTypeDelivery:
		return []string{
			"Definitions / Glossary",
			"User Stories",
			"Acceptance Criteria",
			"Data & Interfaces",
			"Constraints",
			"Verification Plan",
			"Non Goals",
			"Open Questions",
		}
	default:
		return nil
	}
}
