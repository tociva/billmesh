package main

import (
	"bufio"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

func main() {
	if len(os.Args) != 2 {
		fail("usage: go run ./scripts/verify-route-coverage.go TRACE_FILE")
	}
	source, err := os.ReadFile("internal/app/api.go")
	if err != nil {
		fail("read API routes: %v", err)
	}
	registrations := regexp.MustCompile(`(?:mux|protected)\.HandleFunc\("([A-Z]+) ([^\"]+)"`).FindAllStringSubmatch(string(source), -1)
	if len(registrations) < 45 {
		fail("only %d API routes were discovered", len(registrations))
	}
	mux := http.NewServeMux()
	wanted := make(map[string]bool, len(registrations))
	for _, registration := range registrations {
		pattern := registration[1] + " " + registration[2]
		mux.HandleFunc(pattern, func(http.ResponseWriter, *http.Request) {})
		wanted[pattern] = false
	}
	file, err := os.Open(os.Args[1])
	if err != nil {
		fail("open route trace: %v", err)
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		fields := strings.Fields(scanner.Text())
		if len(fields) != 3 {
			fail("malformed route trace line: %q", scanner.Text())
		}
		status, err := strconv.Atoi(fields[1])
		if err != nil {
			fail("malformed route status: %q", fields[1])
		}
		if status < 200 || status >= 300 {
			continue
		}
		req := &http.Request{Method: fields[0], URL: &url.URL{Path: fields[2]}}
		_, pattern := mux.Handler(req)
		if pattern != "" {
			wanted[pattern] = true
		}
	}
	if err := scanner.Err(); err != nil {
		fail("read route trace: %v", err)
	}
	var missing []string
	for pattern, covered := range wanted {
		if !covered {
			missing = append(missing, pattern)
		}
	}
	sort.Strings(missing)
	if len(missing) > 0 {
		fail("%d of %d routes lack a successful E2E request:\n%s", len(missing), len(wanted), strings.Join(missing, "\n"))
	}
	fmt.Printf("all %d API routes received a successful E2E request\n", len(wanted))
}

func fail(format string, args ...any) {
	fmt.Fprintf(os.Stderr, format+"\n", args...)
	os.Exit(1)
}
