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
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("resolve testkit source path")
	}
	path := filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..", "test-plan.md"))
	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open test plan: %v", err)
	}
	defer file.Close()

	var cases []PlanCase
	var current *PlanCase
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := strings.TrimSpace(strings.Trim(scanner.Text(), "|"))
		line = strings.TrimSpace(line)
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
		t.Fatalf("read test plan: %v", err)
	}
	if current != nil && current.Description != "" && (kind == "" || current.Kind == kind) {
		cases = append(cases, *current)
	}
	if len(cases) == 0 {
		t.Fatalf("no %s/%s cases found in test-plan.md", prefix, kind)
	}
	return cases
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
