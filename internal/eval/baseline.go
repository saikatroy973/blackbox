package eval

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// LoadRunResult reads a RunResult previously written by SaveRunResult.
func LoadRunResult(path string) (RunResult, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return RunResult{}, err
	}
	var res RunResult
	if err := json.Unmarshal(body, &res); err != nil {
		return RunResult{}, fmt.Errorf("decode %s: %w", path, err)
	}
	return res, nil
}

// SaveRunResult writes res as indented JSON.
func SaveRunResult(path string, res RunResult) error {
	body, err := json.MarshalIndent(res, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(body, '\n'), 0o644)
}

// Regression describes a case that passed in a baseline but no longer does.
type Regression struct {
	Case   string `json:"case"`
	Reason string `json:"reason"`
}

// Compare reports cases that passed in the baseline and fail in the
// current run. Cases that are new since the baseline are ignored.
func Compare(baseline, current RunResult) []Regression {
	base := make(map[string]CaseResult, len(baseline.Cases))
	for _, c := range baseline.Cases {
		base[c.Name] = c
	}
	var regressions []Regression
	for _, c := range current.Cases {
		b, ok := base[c.Name]
		if !ok || !b.Passed || c.Passed {
			continue
		}
		reason := "failed"
		if len(c.Failures) > 0 {
			reason = strings.Join(c.Failures, "; ")
		} else if c.Error != "" {
			reason = c.Error
		}
		regressions = append(regressions, Regression{Case: c.Name, Reason: reason})
	}
	return regressions
}
