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
	// NoDiffEscalationThreshold is the number of consecutive no-diff iterations
	// before escalating from MEDIUM to HIGH. Default: 2x NoDiffThreshold.
	NoDiffEscalationThreshold int
}

// NewAnomalyDetector creates a detector with default thresholds.
func NewAnomalyDetector() *AnomalyDetector {
	return &AnomalyDetector{
		StagnationThreshold:       3,
		NoDiffThreshold:           3,
		NoDiffEscalationThreshold: 6,
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
		// Check for escalation: if the pattern persists beyond the escalation threshold,
		// escalate from MEDIUM to HIGH to trigger supervisor shutdown.
		severity := "MEDIUM"
		escalation := ad.NoDiffEscalationThreshold
		if escalation <= 0 {
			escalation = threshold * 2
		}
		if len(records) >= escalation {
			escalationTail := records[len(records)-escalation:]
			allEscalated := true
			for _, r := range escalationTail {
				if r.HasDiff {
					allEscalated = false
					break
				}
			}
			if allEscalated {
				severity = "HIGH"
			}
		}

		return []Anomaly{{
			Type:        AnomalyNoDiffRetry,
			Description: fmt.Sprintf("%d consecutive iterations produced no diff", countConsecutiveNoDiff(records)),
			Severity:    severity,
			Detected:    time.Now().UTC(),
		}}
	}

	return nil
}

// countConsecutiveNoDiff counts how many trailing records have HasDiff=false.
func countConsecutiveNoDiff(records []IterationRecord) int {
	count := 0
	for i := len(records) - 1; i >= 0; i-- {
		if records[i].HasDiff {
			break
		}
		count++
	}
	return count
}
