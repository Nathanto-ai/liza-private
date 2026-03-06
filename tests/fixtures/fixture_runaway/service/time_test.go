package service

import (
	"testing"
	"time"
)

// TestTimeService_AlwaysFails is intentionally unfixable.
// It asserts that GetTime() returns a time before 2020, which is impossible.
// This simulates a scenario where no code change can make the test pass,
// forcing the system into runaway detection and safe stop.
func TestTimeService_AlwaysFails(t *testing.T) {
	result := GetTime()
	cutoff := time.Date(2020, 1, 1, 0, 0, 0, 0, time.UTC)
	if result.After(cutoff) {
		t.Errorf("GetTime() = %v, want before %v (this test is intentionally unfixable)", result, cutoff)
	}
}
