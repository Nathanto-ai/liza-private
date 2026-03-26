package commands

import (
	"fmt"
	"os"

	"github.com/liza-mas/liza/internal/specvalidate"
)

// ValidateSpecCommand validates a spec file against the rules for the given type.
func ValidateSpecCommand(specPath string, specType string) error {
	st := specvalidate.SpecType(specType)
	if st != specvalidate.SpecTypeVision && st != specvalidate.SpecTypeDelivery {
		return fmt.Errorf("unknown spec type %q: must be 'vision' or 'delivery'", specType)
	}

	content, err := os.ReadFile(specPath)
	if err != nil {
		return fmt.Errorf("failed to read spec file %s: %w", specPath, err)
	}

	result := specvalidate.ValidateSpecFile(string(content), st)

	if len(result.Warnings) > 0 {
		for _, w := range result.Warnings {
			fmt.Fprintf(warnWriter, "WARNING: %s\n", w)
		}
	}

	if !result.Valid {
		return fmt.Errorf("spec validation failed: missing sections: %v", result.Missing)
	}

	return nil
}
