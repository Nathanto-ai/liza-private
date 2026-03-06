package specvalidate

import (
	"testing"
)

func TestParseSections(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		content   string
		wantCount int
		wantNames []string
	}{
		{
			name:      "no headings",
			content:   "just some text\nwithout headings",
			wantCount: 0,
		},
		{
			name: "single heading",
			content: `## Problem Statement
This is the problem.`,
			wantCount: 1,
			wantNames: []string{"Problem Statement"},
		},
		{
			name: "multiple headings",
			content: `## Section One
Content one.

## Section Two
Content two.

## Section Three
Content three.
`,
			wantCount: 3,
			wantNames: []string{"Section One", "Section Two", "Section Three"},
		},
		{
			name: "mixed heading levels",
			content: `# Top Level
Intro.

## Sub Section
Content.

### Sub Sub Section
More content.
`,
			wantCount: 3,
			wantNames: []string{"Top Level", "Sub Section", "Sub Sub Section"},
		},
		{
			name:      "empty content",
			content:   "",
			wantCount: 0,
		},
		{
			name: "headings with trailing hashes",
			content: `## Section Name ##
Content.`,
			wantCount: 1,
			wantNames: []string{"Section Name"},
		},
		{
			name: "empty section content",
			content: `## Section One

## Section Two
Has content.`,
			wantCount: 2,
			wantNames: []string{"Section One", "Section Two"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			sections := parseSections(tt.content)

			if len(sections) != tt.wantCount {
				t.Errorf("parseSections returned %d sections, want %d", len(sections), tt.wantCount)
			}

			for i, name := range tt.wantNames {
				if i >= len(sections) {
					break
				}
				if sections[i].Name != name {
					t.Errorf("section[%d].Name = %q, want %q", i, sections[i].Name, name)
				}
			}
		})
	}
}

func TestParseHeading(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		line      string
		wantLevel int
		wantName  string
	}{
		{name: "h1", line: "# Title", wantLevel: 1, wantName: "Title"},
		{name: "h2", line: "## Section", wantLevel: 2, wantName: "Section"},
		{name: "h3", line: "### Subsection", wantLevel: 3, wantName: "Subsection"},
		{name: "not a heading", line: "regular text", wantLevel: 0, wantName: ""},
		{name: "empty string", line: "", wantLevel: 0, wantName: ""},
		{name: "trailing hashes", line: "## Title ##", wantLevel: 2, wantName: "Title"},
		{name: "h4 above threshold ignored in parseSections", line: "#### Deep", wantLevel: 4, wantName: "Deep"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			level, name := parseHeading(tt.line)
			if level != tt.wantLevel {
				t.Errorf("parseHeading(%q) level = %d, want %d", tt.line, level, tt.wantLevel)
			}
			if name != tt.wantName {
				t.Errorf("parseHeading(%q) name = %q, want %q", tt.line, name, tt.wantName)
			}
		})
	}
}

func TestHasGivenWhenThen(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		content string
		want    bool
	}{
		{
			name:    "complete GWT",
			content: "Given: a user\nWhen: they login\nThen: they see dashboard",
			want:    true,
		},
		{
			name:    "missing then",
			content: "Given: a user\nWhen: they login",
			want:    false,
		},
		{
			name:    "missing given",
			content: "When: they login\nThen: they see dashboard",
			want:    false,
		},
		{
			name:    "case insensitive",
			content: "given: a user\nwhen: they login\nthen: they see dashboard",
			want:    true,
		},
		{
			name:    "empty",
			content: "",
			want:    false,
		},
		{
			name:    "no GWT at all",
			content: "The system should just work.",
			want:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			if got := hasGivenWhenThen(tt.content); got != tt.want {
				t.Errorf("hasGivenWhenThen(%q) = %v, want %v", tt.content, got, tt.want)
			}
		})
	}
}
