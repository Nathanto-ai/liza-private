package runtime

import (
	"fmt"
	"time"
)

// AnomalyType classifies what kind of anomaly was detected.
type AnomalyType string

const (
	AnomalyStagnation    AnomalyType = "STAGNATION"
	AnomalyRepeatFailure AnomalyType = "REPEAT_FAILURE"
	AnomalyNoDiffRetry   AnomalyType = "NO_DIFF_RETRY"
)

// Anomaly represents a detected runaway or stagnation pattern.
type Anomaly struct {
	Type        AnomalyType
	Description string
	Severity    string // HIGH, MEDIUM, LOW
	Detected    time.Time
}

// IterationRecord captures one iteration's outcome for anomaly detection.
type IterationRecord struct {
	TaskID    string
	Status    string
	HasDiff   bool
	Timestamp time.Time
}

// AnomalyDetector examines iteration history for concerning patterns.
type AnomalyDetector struct {
	// StagnationThreshold is the number of consecutive same-task failures
	// before flagging stagnation. Default: 3.
	StagnationThreshold int
	// NoDiffThreshold is the number of consecutive no-diff iterations
	// before flagging. Default: 3.
	NoDiffThreshold int
}

// NewAnomalyDetector creates a detector with default thresholds.
func NewAnomalyDetector() *AnomalyDetector {
	return &AnomalyDetector{
		StagnationThreshold: 3,
		NoDiffThreshold:     3,
	}
}

// Detect examines the iteration history and returns any anomalies found.
func (ad *AnomalyDetector) Detect(records []IterationRecord) []Anomaly {
	var anomalies []Anomaly

	anomalies = append(anomalies, ad.detectStagnation(records)...)
	anomalies = append(anomalies, ad.detectNoDiffRetries(records)...)

	return anomalies
}

// detectStagnation looks for repeated failures on the same task.
func (ad *AnomalyDetector) detectStagnation(records []IterationRecord) []Anomaly {
	if len(records) < ad.StagnationThreshold {
		return nil
	}

	// Check last N records for same-task repeated failures
	threshold := ad.StagnationThreshold
	tail := records[len(records)-threshold:]

	taskID := tail[0].TaskID
	if taskID == "" {
		return nil
	}

	allSameTask := true
	allFailed := true
	for _, r := range tail {
		if r.TaskID != taskID {
			allSameTask = false
			break
		}
		if r.Status != "FAILED" && r.Status != "BLOCKED" {
			allFailed = false
		}
	}

	if allSameTask && allFailed {
		return []Anomaly{{
			Type:        AnomalyStagnation,
			Description: fmt.Sprintf("task %s failed/blocked %d consecutive times", taskID, threshold),
			Severity:    "HIGH",
			Detected:    time.Now().UTC(),
		}}
	}

	return nil
}

// detectNoDiffRetries looks for repeated iterations that produce no diff.
func (ad *AnomalyDetector) detectNoDiffRetries(records []IterationRecord) []Anomaly {
	if len(records) < ad.NoDiffThreshold {
		return nil
	}

	threshold := ad.NoDiffThreshold
	tail := records[len(records)-threshold:]

	allNoDiff := true
	for _, r := range tail {
		if r.HasDiff {
			allNoDiff = false
			break
		}
	}

	if allNoDiff {
		return []Anomaly{{
			Type:        AnomalyNoDiffRetry,
			Description: fmt.Sprintf("%d consecutive iterations produced no diff", threshold),
			Severity:    "MEDIUM",
			Detected:    time.Now().UTC(),
		}}
	}

	return nil
}
