package commands

import (
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/liza-mas/liza/internal/db"
	"github.com/liza-mas/liza/internal/models"
	"github.com/liza-mas/liza/internal/testhelpers"
)

func TestResumeCommand(t *testing.T) {
	tests := []struct {
		name        string
		initialMode models.SystemMode
		changedBy   string
		wantErr     bool
		errContains string
		wantMode    models.SystemMode
	}{
		{
			name:        "resume from PAUSED mode",
			initialMode: models.SystemModePaused,
			changedBy:   "human",
			wantErr:     false,
			wantMode:    models.SystemModeRunning,
		},
		{
			name:        "resume from PAUSED with agent ID",
			initialMode: models.SystemModePaused,
			changedBy:   "admin-1",
			wantErr:     false,
			wantMode:    models.SystemModeRunning,
		},
		{
			name:        "cannot resume from RUNNING",
			initialMode: models.SystemModeRunning,
			changedBy:   "human",
			wantErr:     true,
			errContains: "not PAUSED",
			wantMode:    models.SystemModeRunning,
		},
		{
			name:        "cannot resume from STOPPED",
			initialMode: models.SystemModeStopped,
			changedBy:   "human",
			wantErr:     true,
			errContains: "cannot resume from STOPPED",
			wantMode:    models.SystemModeStopped,
		},
		{
			name:        "cannot resume from empty mode",
			initialMode: "",
			changedBy:   "human",
			wantErr:     true,
			errContains: "not PAUSED",
			wantMode:    "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Setup test environment
			tmpDir := t.TempDir()
			stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)
			testhelpers.SetupPipelineConfig(t, tmpDir)

			// Create initial state with specified mode
			state := testhelpers.CreateValidState()
			state.Config.Mode = tt.initialMode

			bb := testhelpers.WriteInitialState(t, stateFile, state)

			// Run resume command
			err := ResumeCommand(tmpDir, tt.changedBy)

			// Check error
			if tt.wantErr {
				if err == nil {
					t.Errorf("Expected error but got none")
					return
				}
				testhelpers.AssertErrorContains(t, err, tt.errContains)

				// Verify mode didn't change
				updatedState, readErr := bb.Read()
				if readErr != nil {
					t.Fatalf("Failed to read state: %v", readErr)
				}
				if updatedState.Config.Mode != tt.wantMode {
					t.Errorf("Mode should not have changed: got %v, want %v", updatedState.Config.Mode, tt.wantMode)
				}
				return
			}

			testhelpers.AssertNoError(t, err)

			// Verify mode was updated
			updatedState, err := bb.Read()
			if err != nil {
				t.Fatalf("Failed to read state: %v", err)
			}

			if updatedState.Config.Mode != tt.wantMode {
				t.Errorf("Mode = %v, want %v", updatedState.Config.Mode, tt.wantMode)
			}

			// Verify mode_changed_at was set
			if updatedState.Config.ModeChangedAt == nil {
				t.Error("ModeChangedAt should be set")
			}

			// Verify mode_changed_by was set
			if updatedState.Config.ModeChangedBy == nil {
				t.Error("ModeChangedBy should be set")
			} else if *updatedState.Config.ModeChangedBy != tt.changedBy {
				t.Errorf("ModeChangedBy = %v, want %v", *updatedState.Config.ModeChangedBy, tt.changedBy)
			}
		})
	}
}

// TestResumeCommand_ArchiveWriteFailure verifies that when the archive file
// cannot be written, resume fails and state remains unchanged (no data loss).
// Uses COMPLETED sprint status because the two-step flow is:
//
//	CHECKPOINT + all terminal → COMPLETED (no archive), then
//	COMPLETED → archive + new sprint (archive write happens here).
func TestResumeCommand_ArchiveWriteFailure(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Windows does not enforce Unix directory permission bits via os.Chmod")
	}
	tmpDir := t.TempDir()
	stateFile, _ := testhelpers.SetupLizaDir(t, tmpDir)
	testhelpers.SetupPipelineConfig(t, tmpDir)

	// Make archive directory unwritable so writeSprintArchive fails.
	archiveDir := filepath.Join(tmpDir, ".liza", "archive")
	if err := os.MkdirAll(archiveDir, 0755); err != nil {
		t.Fatalf("Failed to create archive dir: %v", err)
	}
	if err := os.Chmod(archiveDir, 0444); err != nil {
		t.Fatalf("Failed to chmod archive dir: %v", err)
	}
	t.Cleanup(func() { os.Chmod(archiveDir, 0755) })

	now := time.Now().UTC()
	state := testhelpers.CreateValidState()
	state.Config.Mode = models.SystemModeRunning
	state.Sprint.Status = models.SprintStatusCompleted
	state.Sprint.Number = 1

	mergedTask := testhelpers.BuildTaskByStatus("task-1", models.TaskStatusMerged, now)
	state.Tasks = []models.Task{mergedTask}
	state.Sprint.Scope.Planned = []string{"task-1"}

	testhelpers.WriteInitialState(t, stateFile, state)

	// Resume should fail because archive write fails before state mutation.
	err := ResumeCommand(tmpDir, "human")
	if err == nil {
		t.Fatal("ResumeCommand() should fail when archive write fails")
	}

	// Verify state is unchanged — sprint was NOT advanced.
	bb := db.New(stateFile)
	readState, readErr := bb.Read()
	if readErr != nil {
		t.Fatalf("Failed to read state: %v", readErr)
	}
	if readState.Sprint.ID != "sprint-1" {
		t.Errorf("Sprint.ID = %q, want %q (should be unchanged)", readState.Sprint.ID, "sprint-1")
	}
	if readState.Sprint.Number != 1 {
		t.Errorf("Sprint.Number = %d, want 1 (should be unchanged)", readState.Sprint.Number)
	}
	if readState.Sprint.Status != models.SprintStatusCompleted {
		t.Errorf("Sprint.Status = %v, want COMPLETED (should be unchanged)", readState.Sprint.Status)
	}
	if len(readState.SprintHistory) != 0 {
		t.Errorf("SprintHistory length = %d, want 0 (should be unchanged)", len(readState.SprintHistory))
	}
}
