package testkit

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

type PlanCase struct {
	ID          string
	Kind        string
	Description string
}

func Cases(t testing.TB, prefix, kind string) []PlanCase {
	t.Helper()
	cases, err := cases(prefix, kind)
	if err != nil {
		t.Fatalf("%v", err)
	}
	if len(cases) == 0 {
		t.Fatalf("no %s/%s cases found in test-plan.md", prefix, kind)
	}
	return cases
}

func OptionalCases(t testing.TB, prefix, kind string) []PlanCase {
	t.Helper()
	cases, err := cases(prefix, kind)
	if err != nil {
		t.Fatalf("%v", err)
	}
	return cases
}

func cases(prefix, kind string) ([]PlanCase, error) {
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		return nil, fmt.Errorf("resolve testkit source path")
	}
	path := filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..", "docs", "test-plan.md"))
	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open test plan: %w", err)
	}
	defer file.Close()

	var cases []PlanCase
	var current *PlanCase
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(strings.Trim(scanner.Text(), "|"))
		line = strings.TrimSpace(line)
		parts := strings.Split(line, "|")
		if len(parts) >= 3 {
			id := strings.TrimSpace(parts[0])
			caseKind := strings.TrimSpace(parts[1])
			description := strings.TrimSpace(parts[2])
			if strings.HasPrefix(id, prefix+"-") && description != "" {
				if current != nil && current.Description != "" && (kind == "" || current.Kind == kind) {
					cases = append(cases, *current)
				}
				current = nil
				if kind == "" || caseKind == kind {
					cases = append(cases, PlanCase{ID: id, Kind: caseKind, Description: description})
				}
				continue
			}
		}
		if strings.HasPrefix(line, prefix+"-") {
			if current != nil && current.Description != "" && (kind == "" || current.Kind == kind) {
				cases = append(cases, *current)
			}
			current = &PlanCase{ID: line}
			continue
		}
		if current == nil || line == "" || strings.HasPrefix(line, "---") {
			continue
		}
		if current.Kind == "" && (line == "U" || line == "I" || line == "E" || line == "P") {
			current.Kind = line
			continue
		}
		if current.Kind != "" && current.Description == "" {
			current.Description = line
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read test plan: %w", err)
	}
	if current != nil && current.Description != "" && (kind == "" || current.Kind == kind) {
		cases = append(cases, *current)
	}
	return cases, nil
}

func RunCases(t *testing.T, prefix, kind string, run func(*testing.T, PlanCase)) {
	t.Helper()
	for _, tc := range Cases(t, prefix, kind) {
		tc := tc
		t.Run(fmt.Sprintf("%s_%s", tc.ID, slug(tc.Description)), func(t *testing.T) { run(t, tc) })
	}
}

func slug(s string) string {
	fields := strings.FieldsFunc(strings.ToLower(s), func(r rune) bool { return !(r >= 'a' && r <= 'z') && !(r >= '0' && r <= '9') })
	if len(fields) > 8 {
		fields = fields[:8]
	}
	return strings.Join(fields, "_")
}
