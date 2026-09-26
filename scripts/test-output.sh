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

cleanup() {
	rm -f "$output_log" "$status_file"
}
trap cleanup EXIT INT TERM

if [ -n "${NO_COLOR:-}" ]; then
	red=''
	yellow=''
	bold=''
	reset=''
else
	red=$(printf '\033[31m')
	yellow=$(printf '\033[33m')
	bold=$(printf '\033[1m')
	reset=$(printf '\033[0m')
fi

# Store the command status separately because the display pipeline has its own
# exit status. tee retains the complete raw output for the focused summary.
(
	"$@"
	printf '%s\n' "$?" >"$status_file"
) 2>&1 | tee "$output_log" | awk -v red="$red" -v yellow="$yellow" -v reset="$reset" '
	/--- FAIL:|(^|[[:space:]])FAIL([[:space:]]|$)|panic:|fatal error:/ {
		print red $0 reset
		next
	}
	/Error Trace:|Error:|Messages:|expected:|actual[[:space:]]*:|Test:|[.]go:[0-9]+:/ {
		print yellow $0 reset
		next
	}
	{ print }
	{ fflush() }
'

if [ ! -s "$status_file" ]; then
	command_status=1
else
	command_status=$(cat "$status_file")
fi

if [ "$command_status" -ne 0 ]; then
	printf '\n%s%s%s%s\n' "$bold" "$red" "$suite_name failure details" "$reset"
	awk '
		/--- FAIL:|(^|[[:space:]])FAIL([[:space:]]|$)|panic:|fatal error:|Error Trace:|Error:|Messages:|expected:|actual[[:space:]]*:|Test:|[.]go:[0-9]+:/ {
			print
		}
	' "$output_log" | awk -v red="$red" -v reset="$reset" '{ print red $0 reset }'
	printf '%sCommand:%s' "$bold" "$reset"
	printf ' %s' "$@"
	printf '\n%sExit status:%s %s\n' "$bold" "$reset" "$command_status"
fi

exit "$command_status"
