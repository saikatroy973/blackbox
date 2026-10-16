// Package eval runs YAML test suites against agents, asserts on their
// behavior, and gates changes on regressions against a baseline run.
package eval

import (
	"fmt"
	"io"
	"os"

	"gopkg.in/yaml.v3"
)

// Suite is a collection of eval cases.
type Suite struct {
	Name  string `yaml:"suite"`
	Cases []Case `yaml:"cases"`
}

// Case is a single question with expectations.
type Case struct {
	Name     string `yaml:"name"`
	Question string `yaml:"question"`
	Expect   Expect `yaml:"expect"`
}

// Expect lists the assertions a case must satisfy to pass.
type Expect struct {
	OutputContains    []string `yaml:"output_contains"`
	OutputNotContains []string `yaml:"output_not_contains"`
	ToolsCalled       []string `yaml:"tools_called"`
	MaxSteps          int      `yaml:"max_steps"`
	MaxCostUSD        float64  `yaml:"max_cost_usd"`
	MaxDurationMS     int64    `yaml:"max_duration_ms"`
}

// LoadSuite reads and validates a suite YAML file.
func LoadSuite(path string) (Suite, error) {
	f, err := os.Open(path)
	if err != nil {
		return Suite{}, err
	}
	defer func() { _ = f.Close() }()
	return ParseSuite(f)
}

// ParseSuite decodes and validates a suite.
func ParseSuite(r io.Reader) (Suite, error) {
	dec := yaml.NewDecoder(r)
	dec.KnownFields(true)
	var s Suite
	if err := dec.Decode(&s); err != nil {
		return Suite{}, fmt.Errorf("decode suite: %w", err)
	}
	if err := s.Validate(); err != nil {
		return Suite{}, err
	}
	return s, nil
}

// Validate checks the suite structure.
func (s Suite) Validate() error {
	if s.Name == "" {
		return fmt.Errorf("suite is missing a name")
	}
	if len(s.Cases) == 0 {
		return fmt.Errorf("suite %q has no cases", s.Name)
	}
	seen := make(map[string]bool, len(s.Cases))
	for i, c := range s.Cases {
		if c.Name == "" {
			return fmt.Errorf("case %d is missing a name", i+1)
		}
		if seen[c.Name] {
			return fmt.Errorf("duplicate case name %q", c.Name)
		}
		seen[c.Name] = true
		if c.Question == "" {
			return fmt.Errorf("case %q is missing a question", c.Name)
		}
	}
	return nil
}
