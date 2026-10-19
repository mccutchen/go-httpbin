#!/bin/bash
# Test runner for the _delay task.
#   ./test.sh [--output_path <junit.xml>] base|new
# base: the existing test suite (regression check).
# new:  the tests for the new behavior.

MODE=""
OUTPUT_PATH=""

while [[ $# -gt 0 ]]; do
  case $1 in
    --output_path)
      OUTPUT_PATH="$2"
      shift 2
      ;;
    base|new)
      MODE="$1"
      shift
      ;;
    *)
      echo "Unknown argument: $1"
      exit 1
      ;;
  esac
done

if [ -z "$MODE" ]; then
  echo "Usage: $0 [--output_path <path>] [base|new]"
  exit 1
fi

cd "$(dirname "$0")" || exit 1

NEW_TESTS='^TestRequestDelay$'

run_tests() {
  if [ -n "$OUTPUT_PATH" ]; then
    go test -count=1 -v "$@" 2>&1 | go-junit-report -iocopy -out "$OUTPUT_PATH"
    return "${PIPESTATUS[0]}"
  fi
  go test -count=1 -v "$@"
}

if [ "$MODE" = "base" ]; then
  run_tests -skip "$NEW_TESTS" ./...
else
  run_tests -run "$NEW_TESTS" ./httpbin/
fi
