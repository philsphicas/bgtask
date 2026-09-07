#!/usr/bin/env bash
set -euo pipefail

script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
temp_dir="$(mktemp -d)"
trap 'rm -rf -- "$temp_dir"' EXIT
cd "$temp_dir"

# Stub only Go; exercise the actual updater, metadata parsing, and version sorting.
go() {
  [[ "$GOTOOLCHAIN" == local ]] || return 90
  if [[ "$*" == "env GOVERSION" ]]; then
    printf 'go%s\n' "$TEST_LATEST"
  else
    printf '%s\n' "$*" >> "$TEST_CALLS"
    if [[ "$1 $2" == "mod tidy" ]]; then
      return "${TEST_TIDY_STATUS:-0}"
    fi
  fi
}
export -f go
export TEST_LATEST TEST_CALLS="$temp_dir/calls"

check() {
  local name="$1" current="$2" toolchain="$3" latest="$4" available="$5" patch="${6:-}"
  printf 'module example.com/test\n\ngo %s\n' "$current" > go.mod
  if [[ -n "$toolchain" ]]; then
    printf '\ntoolchain %s\n' "$toolchain" >> go.mod
  fi
  TEST_LATEST="$latest"
  local result
  result="$(bash "$script_dir/update-go-toolchain.sh" check)"
  grep -qx "update-available=$available" <<< "$result"
  if [[ "$available" == true ]]; then
    grep -qx "current=$current" <<< "$result"
    grep -qx "latest=$latest" <<< "$result"
    grep -qx "is-patch=$patch" <<< "$result"
  fi
  echo "PASS: $name"
}

check "patch release" 1.27.0 "" 1.27.1 true true
check "feature release" 1.26.7 "" 1.27.1 true false
check "major release" 1.27.1 "" 2.0.0 true false
check "already current" 1.27.1 "" 1.27.1 false
check "no downgrade" 1.28.0 "" 1.27.1 false
check "numeric patch ordering" 1.27.9 "" 1.27.10 true true
check "numeric patch downgrade" 1.27.10 "" 1.27.9 false
check "normalize PR 40 metadata" 1.27.0 go1.27.1 1.27.1 true true
check "remove redundant toolchain" 1.27.1 go1.27.1 1.27.1 true true
check "remove default toolchain" 1.27.1 default 1.27.1 true true
check "feature despite preferred toolchain" 1.26.0 go1.27.0 1.27.1 true false
check "no preferred toolchain downgrade" 1.27.0 go1.28.0 1.27.1 false
check "legacy minor-only version" 1.26 "" 1.27.1 true false

for version in "" 1.27rc1 invalid; do
  printf 'module example.com/test\n\ngo %s\n' "$version" > go.mod
  if bash "$script_dir/update-go-toolchain.sh" check > /dev/null 2>&1; then
    echo "FAIL: accepted invalid version '$version'" >&2
    exit 1
  fi
done
echo "PASS: invalid metadata fails"

printf 'module example.com/test\n\ngo 1.27.0\n' > go.mod
TEST_LATEST=1.28rc1
if bash "$script_dir/update-go-toolchain.sh" check > /dev/null 2>&1; then
  echo "FAIL: accepted a prerelease" >&2
  exit 1
fi
echo "PASS: prerelease fails"

TEST_LATEST=1.27.1
bash "$script_dir/update-go-toolchain.sh" update 1.27.1
expected="$(printf '%s\n' 'get go@1.27.1 toolchain@none' 'mod tidy -go=1.27.1' 'mod edit -toolchain=none')"
[[ "$(cat "$TEST_CALLS")" == "$expected" ]]
echo "PASS: exact version update and tidy"

: > "$TEST_CALLS"
if bash "$script_dir/update-go-toolchain.sh" update 1.27.2 > /dev/null 2>&1; then
  echo "FAIL: accepted the wrong installed toolchain" >&2
  exit 1
fi
[[ ! -s "$TEST_CALLS" ]]
echo "PASS: toolchain mismatch fails before editing"

export TEST_TIDY_STATUS=1
if bash "$script_dir/update-go-toolchain.sh" update 1.27.1 > /dev/null 2>&1; then
  echo "FAIL: ignored tidy failure" >&2
  exit 1
fi
[[ "$(wc -l < "$TEST_CALLS")" -eq 2 ]]
echo "PASS: tidy failure stops the update"

# Check the real Go command's edits and idempotence in a dependency-free module.
unset -f go
installed="$(GOTOOLCHAIN=local go env GOVERSION)"
latest="${installed#go}"
printf 'module example.com/test\n\ngo 1.21.0\n\ntoolchain %s\n' "$installed" > go.mod
printf 'package test\n' > test.go
bash "$script_dir/update-go-toolchain.sh" update "$latest"
grep -qx "go $latest" go.mod
if grep -q '^toolchain ' go.mod; then
  echo "FAIL: toolchain directive remains after update" >&2
  exit 1
fi
result="$(bash "$script_dir/update-go-toolchain.sh" check)"
[[ "$result" == "update-available=false" ]]
echo "PASS: real module update is normalized and idempotent"
