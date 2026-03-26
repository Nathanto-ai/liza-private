package specvalidate

import "strings"

// Section represents a parsed markdown section with its heading name and body content.
type Section struct {
	Name    string
	Content string
	Level   int // heading level (1 = #, 2 = ##, etc.)
}

// parseSections extracts sections from markdown content.
// It splits on ## headings (level 2) by default, collecting the content
// under each heading into a Section struct.
func parseSections(content string) []Section {
	lines := strings.Split(content, "\n")
	var sections []Section
	var current *Section

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		level, name := parseHeading(trimmed)
		if level > 0 && level <= 3 {
			// Start a new section
			if current != nil {
				sections = append(sections, *current)
			}
			current = &Section{
				Name:  name,
				Level: level,
			}
		} else if current != nil {
			current.Content += line + "\n"
		}
	}

	if current != nil {
		sections = append(sections, *current)
	}

	return sections
}

// parseHeading checks if a line is a markdown heading and returns its level and text.
// Returns (0, "") if the line is not a heading.
func parseHeading(line string) (int, string) {
	if !strings.HasPrefix(line, "#") {
		return 0, ""
	}

	level := 0
	for _, ch := range line {
		if ch == '#' {
			level++
		} else {
			break
		}
	}

	if level > 6 {
		return 0, ""
	}

	name := strings.TrimSpace(line[level:])
	// Remove any trailing # characters (alternate heading syntax)
	name = strings.TrimRight(name, "# ")
	return level, name
}
