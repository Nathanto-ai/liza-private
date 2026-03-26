package db

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/models"
)

// Regression tests for V&V Run 7 Fix 39: Pre-write state backup and auto-recovery.

// TestFix39_WriteCreatesBackup verifies that writeStateData creates a .bak
// file before overwriting state.yaml.
// Bug: state.yaml corruption on concurrent writes caused unrecoverable
// data loss (ISSUE-R7-09).
func TestFix39_WriteCreatesBackup(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.yaml")
	now := time.Now().UTC()

	state := &models.State{
		Version: 1,
		Goal: models.Goal{
			ID:          "goal-1",
			Description: "Test goal",
			SpecRef:     "spec.md",
			Created:     now,
			Status:      models.GoalStatusInProgress,
		},
		Tasks:  []models.Task{},
		Agents: make(map[string]models.Agent),
		Config: models.Config{IntegrationBranch: "main"},
	}

	bb := New(statePath)

	// First write — no backup yet (no prior file)
	if err := bb.Write(state); err != nil {
		t.Fatalf("first Write failed: %v", err)
	}

	// Second write — should create .bak from first write
	state.Version = 2
	if err := bb.Write(state); err != nil {
		t.Fatalf("second Write failed: %v", err)
	}

	backupPath := statePath + ".bak"
	if _, err := os.Stat(backupPath); os.IsNotExist(err) {
		t.Fatal("expected .bak file to exist after second write")
	}

	// Backup should contain version 1 (the pre-overwrite content)
	backupBB := New(backupPath)
	backupState, err := backupBB.Read()
	if err != nil {
		t.Fatalf("failed to read backup: %v", err)
	}
	if backupState.Version != 1 {
		t.Errorf("backup version = %d, want 1 (pre-overwrite)", backupState.Version)
	}

	// Main file should have version 2
	readState, err := bb.Read()
	if err != nil {
		t.Fatalf("Read failed: %v", err)
	}
	if readState.Version != 2 {
		t.Errorf("main version = %d, want 2", readState.Version)
	}
}

// TestFix39_ReadRecoveryFromBackup verifies that Read() auto-recovers
// from the .bak file when the main state.yaml is missing.
func TestFix39_ReadRecoveryFromBackup(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.yaml")
	backupPath := statePath + ".bak"
	now := time.Now().UTC()

	state := &models.State{
		Version: 1,
		Goal: models.Goal{
			ID:          "goal-1",
			Description: "Test goal",
			SpecRef:     "spec.md",
			Created:     now,
			Status:      models.GoalStatusInProgress,
		},
		Tasks:  []models.Task{},
		Agents: make(map[string]models.Agent),
		Config: models.Config{IntegrationBranch: "main"},
	}

	// Write the backup file directly
	backupBB := New(backupPath)
	if err := backupBB.Write(state); err != nil {
		t.Fatalf("backup Write failed: %v", err)
	}

	// Main file does NOT exist — Read should recover from backup
	bb := New(statePath)
	readState, err := bb.Read()
	if err != nil {
		t.Fatalf("Read (with backup recovery) failed: %v", err)
	}
	if readState.Version != 1 {
		t.Errorf("recovered version = %d, want 1", readState.Version)
	}

	// After recovery, main file should be restored
	if _, err := os.Stat(statePath); os.IsNotExist(err) {
		t.Error("expected main file to be restored from backup")
	}
}

// TestFix39_ReadRecoveryFromEmptyFile verifies that Read() auto-recovers
// from the .bak file when state.yaml exists but is empty (truncated).
func TestFix39_ReadRecoveryFromEmptyFile(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.yaml")
	backupPath := statePath + ".bak"
	now := time.Now().UTC()

	state := &models.State{
		Version: 1,
		Goal: models.Goal{
			ID:          "goal-1",
			Description: "Test goal",
			SpecRef:     "spec.md",
			Created:     now,
			Status:      models.GoalStatusInProgress,
		},
		Tasks:  []models.Task{},
		Agents: make(map[string]models.Agent),
		Config: models.Config{IntegrationBranch: "main"},
	}

	// Create empty main file
	if err := os.WriteFile(statePath, []byte{}, 0644); err != nil {
		t.Fatalf("failed to write empty file: %v", err)
	}

	// Write valid backup
	backupBB := New(backupPath)
	if err := backupBB.Write(state); err != nil {
		t.Fatalf("backup Write failed: %v", err)
	}

	bb := New(statePath)
	readState, err := bb.Read()
	if err != nil {
		t.Fatalf("Read (empty file with backup recovery) failed: %v", err)
	}
	if readState.Version != 1 {
		t.Errorf("recovered version = %d, want 1", readState.Version)
	}
}

// TestFix39_ReadFailsWithoutBackup verifies that Read() still fails
// when both main and backup files are missing.
func TestFix39_ReadFailsWithoutBackup(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	statePath := filepath.Join(dir, "nonexistent.yaml")

	bb := New(statePath)
	_, err := bb.Read()
	if err == nil {
		t.Fatal("expected Read to fail when no file and no backup exist")
	}
}
