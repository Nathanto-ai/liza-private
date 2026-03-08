package runtime

import (
	"testing"
	"time"
)

func TestAnomalyDetector_Stagnation(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		records   []IterationRecord
		threshold int
		wantCount int
		wantType  AnomalyType
	}{
		{
			name:      "no records returns empty",
			records:   nil,
			threshold: 3,
			wantCount: 0,
		},
		{
			name: "repeated failures on same task detected",
			records: []IterationRecord{
				{TaskID: "task-1", Status: "FAILED", Timestamp: time.Now()},
				{TaskID: "task-1", Status: "FAILED", Timestamp: time.Now()},
				{TaskID: "task-1", Status: "FAILED", Timestamp: time.Now()},
			},
			threshold: 3,
			wantCount: 1,
			wantType:  AnomalyStagnation,
		},
		{
			name: "repeated blocked on same task detected",
			records: []IterationRecord{
				{TaskID: "task-2", Status: "BLOCKED", Timestamp: time.Now()},
				{TaskID: "task-2", Status: "BLOCKED", Timestamp: time.Now()},
				{TaskID: "task-2", Status: "BLOCKED", Timestamp: time.Now()},
			},
			threshold: 3,
			wantCount: 1,
			wantType:  AnomalyStagnation,
		},
		{
			name: "mixed tasks not detected",
			records: []IterationRecord{
				{TaskID: "task-1", Status: "FAILED", Timestamp: time.Now()},
				{TaskID: "task-2", Status: "FAILED", Timestamp: time.Now()},
				{TaskID: "task-1", Status: "FAILED", Timestamp: time.Now()},
			},
			threshold: 3,
			wantCount: 0,
		},
		{
			name: "success breaks the pattern",
			records: []IterationRecord{
				{TaskID: "task-1", Status: "FAILED", Timestamp: time.Now()},
				{TaskID: "task-1", Status: "MERGED", Timestamp: time.Now()},
				{TaskID: "task-1", Status: "FAILED", Timestamp: time.Now()},
			},
			threshold: 3,
			wantCount: 0,
		},
		{
			name: "below threshold not detected",
			records: []IterationRecord{
				{TaskID: "task-1", Status: "FAILED", Timestamp: time.Now()},
				{TaskID: "task-1", Status: "FAILED", Timestamp: time.Now()},
			},
			threshold: 3,
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ad := NewAnomalyDetector()
			ad.StagnationThreshold = tt.threshold

			anomalies := ad.Detect(tt.records)

			// Filter for stagnation anomalies
			var stagnation []Anomaly
			for _, a := range anomalies {
				if a.Type == AnomalyStagnation {
					stagnation = append(stagnation, a)
				}
			}

			if len(stagnation) != tt.wantCount {
				t.Errorf("stagnation anomaly count = %d, want %d", len(stagnation), tt.wantCount)
			}

			if tt.wantCount > 0 && len(stagnation) > 0 {
				if stagnation[0].Type != tt.wantType {
					t.Errorf("type = %q, want %q", stagnation[0].Type, tt.wantType)
				}
				if stagnation[0].Severity != "HIGH" {
					t.Errorf("severity = %q, want HIGH", stagnation[0].Severity)
				}
			}
		})
	}
}

func TestAnomalyDetector_NoDiffRetries(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		records   []IterationRecord
		threshold int
		wantCount int
	}{
		{
			name:      "no records returns empty",
			records:   nil,
			threshold: 3,
			wantCount: 0,
		},
		{
			name: "consecutive no-diff detected",
			records: []IterationRecord{
				{TaskID: "t-1", HasDiff: false, Timestamp: time.Now()},
				{TaskID: "t-2", HasDiff: false, Timestamp: time.Now()},
				{TaskID: "t-3", HasDiff: false, Timestamp: time.Now()},
			},
			threshold: 3,
			wantCount: 1,
		},
		{
			name: "diff in middle breaks pattern",
			records: []IterationRecord{
				{TaskID: "t-1", HasDiff: false, Timestamp: time.Now()},
				{TaskID: "t-2", HasDiff: true, Timestamp: time.Now()},
				{TaskID: "t-3", HasDiff: false, Timestamp: time.Now()},
			},
			threshold: 3,
			wantCount: 0,
		},
		{
			name: "below threshold not detected",
			records: []IterationRecord{
				{TaskID: "t-1", HasDiff: false, Timestamp: time.Now()},
				{TaskID: "t-2", HasDiff: false, Timestamp: time.Now()},
			},
			threshold: 3,
			wantCount: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ad := NewAnomalyDetector()
			ad.NoDiffThreshold = tt.threshold

			anomalies := ad.Detect(tt.records)

			var noDiff []Anomaly
			for _, a := range anomalies {
				if a.Type == AnomalyNoDiffRetry {
					noDiff = append(noDiff, a)
				}
			}

			if len(noDiff) != tt.wantCount {
				t.Errorf("no-diff anomaly count = %d, want %d", len(noDiff), tt.wantCount)
			}

			if tt.wantCount > 0 && len(noDiff) > 0 {
				if noDiff[0].Severity != "MEDIUM" {
					t.Errorf("severity = %q, want MEDIUM", noDiff[0].Severity)
				}
			}
		})
	}
}

func TestAnomalyDetector_CombinedDetection(t *testing.T) {
	t.Parallel()

	// Records that trigger both anomalies: same task failing with no diff
	records := []IterationRecord{
		{TaskID: "t-1", Status: "FAILED", HasDiff: false, Timestamp: time.Now()},
		{TaskID: "t-1", Status: "FAILED", HasDiff: false, Timestamp: time.Now()},
		{TaskID: "t-1", Status: "FAILED", HasDiff: false, Timestamp: time.Now()},
	}

	ad := NewAnomalyDetector()
	anomalies := ad.Detect(records)

	if len(anomalies) != 2 {
		t.Fatalf("expected 2 anomalies, got %d", len(anomalies))
	}

	types := make(map[AnomalyType]bool)
	for _, a := range anomalies {
		types[a.Type] = true
	}
	if !types[AnomalyStagnation] {
		t.Error("expected STAGNATION anomaly")
	}
	if !types[AnomalyNoDiffRetry] {
		t.Error("expected NO_DIFF_RETRY anomaly")
	}
}

func TestAnomalyDetector_NoDiffEscalation(t *testing.T) {
	t.Parallel()

	now := time.Now()

	tests := []struct {
		name         string
		records      []IterationRecord
		wantSeverity string
	}{
		{
			name: "3 no-diffs MEDIUM not escalated",
			records: func() []IterationRecord {
				var recs []IterationRecord
				for i := 0; i < 3; i++ {
					recs = append(recs, IterationRecord{TaskID: "t-1", HasDiff: false, Timestamp: now})
				}
				return recs
			}(),
			wantSeverity: "MEDIUM",
		},
		{
			name: "5 no-diffs still MEDIUM",
			records: func() []IterationRecord {
				var recs []IterationRecord
				for i := 0; i < 5; i++ {
					recs = append(recs, IterationRecord{TaskID: "t-1", HasDiff: false, Timestamp: now})
				}
				return recs
			}(),
			wantSeverity: "MEDIUM",
		},
		{
			name: "6 no-diffs escalated to HIGH",
			records: func() []IterationRecord {
				var recs []IterationRecord
				for i := 0; i < 6; i++ {
					recs = append(recs, IterationRecord{TaskID: "t-1", HasDiff: false, Timestamp: now})
				}
				return recs
			}(),
			wantSeverity: "HIGH",
		},
		{
			name: "7 no-diffs still HIGH",
			records: func() []IterationRecord {
				var recs []IterationRecord
				for i := 0; i < 7; i++ {
					recs = append(recs, IterationRecord{TaskID: "t-1", HasDiff: false, Timestamp: now})
				}
				return recs
			}(),
			wantSeverity: "HIGH",
		},
		{
			name: "diff at position 2 resets escalation",
			records: func() []IterationRecord {
				recs := make([]IterationRecord, 7)
				for i := range recs {
					recs[i] = IterationRecord{TaskID: "t-1", HasDiff: false, Timestamp: now}
				}
				recs[1].HasDiff = true // early diff breaks escalation window
				return recs
			}(),
			wantSeverity: "MEDIUM",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ad := NewAnomalyDetector() // threshold=3, escalation=6

			anomalies := ad.Detect(tt.records)

			var noDiff []Anomaly
			for _, a := range anomalies {
				if a.Type == AnomalyNoDiffRetry {
					noDiff = append(noDiff, a)
				}
			}

			if len(noDiff) != 1 {
				t.Fatalf("expected 1 no-diff anomaly, got %d", len(noDiff))
			}
			if noDiff[0].Severity != tt.wantSeverity {
				t.Errorf("severity = %q, want %q", noDiff[0].Severity, tt.wantSeverity)
			}
		})
	}
}

func TestCountConsecutiveNoDiff(t *testing.T) {
	t.Parallel()

	now := time.Now()

	tests := []struct {
		name    string
		records []IterationRecord
		want    int
	}{
		{"empty", nil, 0},
		{"all no-diff", []IterationRecord{
			{HasDiff: false, Timestamp: now},
			{HasDiff: false, Timestamp: now},
			{HasDiff: false, Timestamp: now},
		}, 3},
		{"trailing no-diff after diff", []IterationRecord{
			{HasDiff: true, Timestamp: now},
			{HasDiff: false, Timestamp: now},
			{HasDiff: false, Timestamp: now},
		}, 2},
		{"ends with diff", []IterationRecord{
			{HasDiff: false, Timestamp: now},
			{HasDiff: true, Timestamp: now},
		}, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			if got := countConsecutiveNoDiff(tt.records); got != tt.want {
				t.Errorf("countConsecutiveNoDiff() = %d, want %d", got, tt.want)
			}
		})
	}
}
