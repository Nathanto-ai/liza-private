package ops

import (
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/liza-mas/liza/internal/git"
)

// isTestFile returns true if the filename matches known test file patterns
// across Go, Python, JS/TS, Shell, Ruby, Java, Kotlin, and Rust.
func isTestFile(name string) bool {
	// Normalize to forward slashes for cross-platform consistency
	name = filepath.ToSlash(name)
	base := filepath.Base(name)

	// Go: *_test.go
	if strings.HasSuffix(base, "_test.go") {
		return true
	}

	// Python: *_test.py, test_*.py
	if strings.HasSuffix(base, "_test.py") || (strings.HasPrefix(base, "test_") && strings.HasSuffix(base, ".py")) {
		return true
	}

	// JS/TS: *.test.{js,ts,jsx,tsx}, *.spec.{js,ts,jsx,tsx}, or any file under __tests__/
	for _, ext := range []string{".js", ".ts", ".jsx", ".tsx"} {
		if strings.HasSuffix(base, ".test"+ext) || strings.HasSuffix(base, ".spec"+ext) {
			return true
		}
		if strings.HasSuffix(base, ext) {
			if strings.Contains(name, "/__tests__/") || strings.HasPrefix(name, "__tests__/") {
				return true
			}
		}
	}

	// Shell: test_*.sh, *_test.sh
	if strings.HasSuffix(base, ".sh") {
		noExt := strings.TrimSuffix(base, ".sh")
		if strings.HasPrefix(noExt, "test_") || strings.HasSuffix(noExt, "_test") {
			return true
		}
	}

	// Ruby: *_test.rb, *_spec.rb
	if strings.HasSuffix(base, "_test.rb") || strings.HasSuffix(base, "_spec.rb") {
		return true
	}

	// Java: *Test.java, *Tests.java — but exclude utility classes like TestUtils, TestConfig
	if strings.HasSuffix(base, ".java") {
		noExt := strings.TrimSuffix(base, ".java")
		if strings.HasSuffix(noExt, "Test") || strings.HasSuffix(noExt, "Tests") {
			if !isTestUtilityClass(noExt) {
				return true
			}
		}
		if strings.HasPrefix(base, "Test") && !isTestUtilityClass(noExt) {
			return true
		}
	}

	// Kotlin: *Test.kt, *Tests.kt — same exclusions
	if strings.HasSuffix(base, ".kt") {
		noExt := strings.TrimSuffix(base, ".kt")
		if strings.HasSuffix(noExt, "Test") || strings.HasSuffix(noExt, "Tests") {
			if !isTestUtilityClass(noExt) {
				return true
			}
		}
		if strings.HasPrefix(base, "Test") && !isTestUtilityClass(noExt) {
			return true
		}
	}

	// Rust: *_test.rs, or any .rs file under a tests/ directory
	if strings.HasSuffix(base, "_test.rs") {
		return true
	}
	if strings.HasSuffix(base, ".rs") {
		if strings.Contains(name, "/tests/") || strings.HasPrefix(name, "tests/") {
			return true
		}
	}

	return false
}

// isTestUtilityClass returns true for Java/Kotlin class names that look like
// test utilities rather than actual test classes (e.g. TestUtils, TestConfig).
func isTestUtilityClass(className string) bool {
	utilitySuffixes := []string{"Utils", "Util", "Helpers", "Helper", "Config", "Configuration", "Factory", "Builder", "Fixture", "Fixtures", "Data", "Base"}
	for _, suffix := range utilitySuffixes {
		if strings.HasSuffix(className, suffix) {
			return true
		}
	}
	return false
}

// HasTestFiles checks whether the commits between baseCommit and HEAD in the
// task worktree include any test files (added or modified).
func HasTestFiles(g *git.Git, taskID, baseCommit string) (bool, error) {
	wtPath := g.GetWorktreePath(taskID)
	files, err := g.DiffFiles(wtPath, baseCommit, "HEAD")
	if err != nil {
		return false, err
	}
	slog.Debug("HasTestFiles: diff files", "task_id", taskID, "base_commit", baseCommit, "file_count", len(files))
	for _, f := range files {
		if isTestFile(f) {
			slog.Debug("HasTestFiles: matched test file", "task_id", taskID, "file", f)
			return true, nil
		}
	}
	slog.Debug("HasTestFiles: no test files found", "task_id", taskID, "files", files)
	return false, nil
}
