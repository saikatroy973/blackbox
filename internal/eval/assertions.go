package eval

import (
	"fmt"
	"strings"
)

// Check evaluates the expectations of a case against its result and
// returns one human-readable failure for every unmet assertion.
func Check(result CaseResult, expect Expect) []string {
	var failures []string
	for _, want := range expect.OutputContains {
		if !strings.Contains(result.Answer, want) {
			failures = append(failures, fmt.Sprintf("output does not contain %q", want))
		}
	}
	for _, banned := range expect.OutputNotContains {
		if strings.Contains(result.Answer, banned) {
			failures = append(failures, fmt.Sprintf("output contains %q", banned))
		}
	}
	for _, tool := range expect.ToolsCalled {
		if !result.usedTool(tool) {
			failures = append(failures, fmt.Sprintf("tool %q was not called (called: %s)", tool, strings.Join(result.Tools, ", ")))
		}
	}
	if expect.MaxSteps > 0 && result.Steps > expect.MaxSteps {
		failures = append(failures, fmt.Sprintf("took %d steps, limit is %d", result.Steps, expect.MaxSteps))
	}
	if expect.MaxCostUSD > 0 && result.CostUSD > expect.MaxCostUSD {
		failures = append(failures, fmt.Sprintf("cost $%.6f exceeds limit $%.6f", result.CostUSD, expect.MaxCostUSD))
	}
	if expect.MaxDurationMS > 0 && result.DurationMS > expect.MaxDurationMS {
		failures = append(failures, fmt.Sprintf("took %dms, limit is %dms", result.DurationMS, expect.MaxDurationMS))
	}
	return failures
}
