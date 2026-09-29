#!/bin/sh

set -u

if [ "$#" -eq 0 ]; then
	printf '%s\n' "usage: $0 COMMAND [ARG ...]" >&2
	exit 2
fi

temporary_root=${TMPDIR:-/tmp}
output_log=$(mktemp "$temporary_root/billmesh-test-output.XXXXXX")
status_file=$(mktemp "$temporary_root/billmesh-test-status.XXXXXX")
suite_name=${TEST_SUITE_NAME:-Tests}
output_style=${TEST_OUTPUT_STYLE:-pretty}

cleanup() {
	rm -f "$output_log" "$status_file"
}
trap cleanup EXIT INT TERM

if [ -n "${NO_COLOR:-}" ]; then
	red=''
	green=''
	yellow=''
	cyan=''
	bold=''
	dim=''
	reset=''
else
	red=$(printf '\033[31m')
	green=$(printf '\033[32m')
	yellow=$(printf '\033[33m')
	cyan=$(printf '\033[36m')
	bold=$(printf '\033[1m')
	dim=$(printf '\033[2m')
	reset=$(printf '\033[0m')
fi

run_command() {
	"$@"
	command_status=$?
	printf '%s\n' "$command_status" >"$status_file"
	return "$command_status"
}

# Go buffers verbose output by package, so completed package chunks can be
# rendered as compact suites without losing assertion or application logs.
if [ "$output_style" = "raw" ]; then
	run_command "$@" 2>&1 | tee "$output_log"
else
	run_command "$@" 2>&1 | tee "$output_log" | awk \
		-v suite="$suite_name" \
		-v red="$red" -v green="$green" -v yellow="$yellow" -v cyan="$cyan" \
		-v bold="$bold" -v dim="$dim" -v reset="$reset" '
	function repeat(value, count, result, i) {
		result = ""
		for (i = 0; i < count; i++) result = result value
		return result
	}
	function test_name(line, value) {
		value = line
		sub(/^=== RUN[[:space:]]+/, "", value)
		sub(/^--- (PASS|FAIL|SKIP):[[:space:]]+/, "", value)
		sub(/[[:space:]]+\([^)]*\)[[:space:]]*$/, "", value)
		return value
	}
	function leaf(name, parts, count) {
		count = split(name, parts, "/")
		return parts[count]
	}
	function depth(name, parts, count) {
		count = split(name, parts, "/")
		return count - 1
	}
	function duration(line, value) {
		value = line
		if (match(value, /\([^)]*\)[[:space:]]*$/)) return substr(value, RSTART, RLENGTH)
		return ""
	}
	function is_detail(line) {
		return line ~ /Error Trace:|Error:|Messages:|expected:|actual[[:space:]]*:|Test:|[.]go:[0-9]+:|panic:|fatal error:/
	}
	function remember_hierarchy(    i, name, parts, count, parent, j, key) {
		for (key in has_child) delete has_child[key]
		for (i = 1; i <= buffered; i++) {
			if (lines[i] !~ /^=== RUN[[:space:]]+/) continue
			name = test_name(lines[i])
			count = split(name, parts, "/")
			parent = parts[1]
			for (j = 2; j <= count; j++) {
				has_child[parent] = 1
				parent = parent "/" parts[j]
			}
		}
	}
	function render_package(package_name, package_status, package_time,    i, line, name, level, label, elapsed, local_passed, local_failed, local_skipped, separator, key) {
		remember_hierarchy()
		print ""
		print bold cyan package_name reset dim "  " package_time reset
		for (i = 1; i <= buffered; i++) {
			line = lines[i]
			if (line ~ /^=== RUN[[:space:]]+/) {
				name = test_name(line)
				if (depth(name) == 0 && has_child[name]) print "  " bold name reset
				continue
			}
			if (line ~ /^=== (PAUSE|CONT)[[:space:]]+/) continue
			if (line ~ /^--- PASS:/) {
				name = test_name(line)
				if (has_child[name]) continue
				level = depth(name)
				elapsed = duration(line)
				print "  " repeat("  ", level) green "PASS" reset " " leaf(name) dim " " elapsed reset
				passed++; local_passed++
				continue
			}
			if (line ~ /^--- SKIP:/) {
				name = test_name(line)
				if (has_child[name]) continue
				level = depth(name)
				elapsed = duration(line)
				print "  " repeat("  ", level) yellow "SKIP" reset " " leaf(name) dim " " elapsed reset
				skipped++; local_skipped++
				continue
			}
			if (line ~ /^--- FAIL:/) {
				name = test_name(line)
				level = depth(name)
				elapsed = duration(line)
				label = has_child[name] ? name " (suite)" : leaf(name)
				print "  " repeat("  ", level) red "FAIL" reset " " label dim " " elapsed reset
				failed++; local_failed++
				continue
			}
			if (line == "PASS" || line == "FAIL" || line ~ /^coverage:/) continue
			# A completed Go benchmark is reported as a result row rather than a
			# "--- PASS:" record. Count that row as a successful case so benchmark-
			# only suites do not misleadingly report zero passes.
			if (line ~ /^Benchmark[^[:space:]]+[[:space:]]+[0-9]+[[:space:]]+/) {
				print "  " cyan line reset
				passed++; local_passed++
				continue
			}
			if (line ~ /^[[:space:]]*$/) continue
			if (is_detail(line)) print "    " yellow line reset
			else print "    " line
		}
		packages++
		if (package_status == "ok") package_passed++
		else package_failed++
		if (local_passed + local_failed + local_skipped > 0) {
			printf "  %s", dim
			if (local_passed > 0) printf "%s passed", local_passed
			separator = local_passed > 0 ? ", " : ""
			if (local_failed > 0) printf "%s%s failed", separator, local_failed
			separator = local_passed + local_failed > 0 ? ", " : ""
			if (local_skipped > 0) printf "%s%s skipped", separator, local_skipped
			printf "%s\n", reset
		}
		for (i = 1; i <= buffered; i++) delete lines[i]
		for (key in has_child) delete has_child[key]
		buffered = 0
	}
	BEGIN { print bold suite reset }
	$1 == "ok" && NF >= 2 {
		render_package($2, "ok", $3)
		next
	}
	$1 == "FAIL" && NF >= 2 {
		render_package($2, "fail", $3)
		next
	}
	$1 == "?" && NF >= 2 {
		if (buffered > 0) render_package($2, "ok", "")
		print ""
		print bold cyan $2 reset dim "  [no test files]" reset
		packages++; package_passed++
		next
	}
	{ lines[++buffered] = $0 }
	END {
		if (buffered > 0) {
			print ""
			print bold red "Command output" reset
			for (i = 1; i <= buffered; i++) {
				if (is_detail(lines[i]) || lines[i] ~ /FAIL|panic:|fatal error:/) print "  " red lines[i] reset
				else print "  " lines[i]
			}
		}
		print ""
		print bold "Summary" reset
		printf "  %s%d passed%s", green, passed, reset
		failure_color = failed > 0 ? red : dim
		skip_color = skipped > 0 ? yellow : dim
		package_failure_color = package_failed > 0 ? red : dim
		printf "  %s%d failed%s", failure_color, failed, reset
		printf "  %s%d skipped%s\n", skip_color, skipped, reset
		printf "  %d packages (%s%d passed%s, %s%d failed%s)\n", packages, green, package_passed, reset, package_failure_color, package_failed, reset
	}
'
fi

if [ ! -s "$status_file" ]; then
	command_status=1
else
	command_status=$(cat "$status_file")
fi

if [ "$command_status" -ne 0 ]; then
	printf '\n%s%s%s%s\n' "$bold" "$red" "$suite_name failure details" "$reset"
	awk '
		/--- FAIL:|(^|[[:space:]])FAIL([[:space:]]|$)|panic:|fatal error:|Error Trace:|Error:|Messages:|expected:|actual[[:space:]]*:|Test:|[.]go:[0-9]+:/ { print }
	' "$output_log" | awk -v red="$red" -v reset="$reset" '{ print red $0 reset }'
	printf '%sCommand:%s' "$bold" "$reset"
	printf ' %s' "$@"
	printf '\n%sExit status:%s %s\n' "$bold" "$reset" "$command_status"
fi

exit "$command_status"
