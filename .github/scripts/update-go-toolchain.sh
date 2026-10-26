#!/usr/bin/env bash
set -euo pipefail

export GOTOOLCHAIN=local

fail() {
  echo "::error::$*" >&2
  exit 1
}

validate_version() {
  [[ "$1" =~ ^[0-9]+\.[0-9]+(\.[0-9]+)?$ ]] \
    || fail "Expected a released Go version, got '$1'."
}

case "${1:-}" in
check)
  # Parse module directives with awk; go mod edit may require a newer compiler.
  current="$(awk '$1 == "go" { print $2 }' go.mod)"
  toolchain="$(awk '$1 == "toolchain" { print $2 }' go.mod)"
  # GOTOOLCHAIN=local reports the installed compiler, not the module's preference.
  latest="$(go env GOVERSION)"
  latest="${latest#go}"
  validate_version "$current"
  validate_version "$latest"

  preferred="$current"
  if [[ -n "$toolchain" && "$toolchain" != default ]]; then
    preferred="${toolchain#go}"
    validate_version "$preferred"
  fi

  if [[ "$(printf '%s\n' "$current" "$preferred" "$latest" | sort -V | tail -n1)" != "$latest" ]] \
    || [[ "$current" == "$latest" && -z "$toolchain" ]]; then
    echo "update-available=false"
    exit 0
  fi

  # Classify against the minimum version, not an already-newer preferred toolchain.
  IFS=. read -r current_major current_minor _ <<<"$current"
  IFS=. read -r latest_major latest_minor _ <<<"$latest"
  is_patch=false
  if [[ "$current_major" == "$latest_major" && "$current_minor" == "$latest_minor" ]]; then
    is_patch=true
  fi

  echo "current=$current"
  echo "latest=$latest"
  echo "is-patch=$is_patch"
  echo "update-available=true"
  ;;
update)
  latest="${2:?Expected the target Go version}"
  validate_version "$latest"
  installed="$(go env GOVERSION)"
  [[ "$installed" == "go$latest" ]] \
    || fail "Expected go$latest, but the installed toolchain is $installed."
  go get "go@$latest" toolchain@none
  go mod tidy -go="$latest"
  # Keep go.mod as the sole version source even if tidy added a toolchain line.
  go mod edit -toolchain=none
  ;;
remediate)
  target="$(awk '$1 == "go" { print $2 }' go.mod)"
  validate_version "$target"
  for attempt in 1 2 3 4; do
    report="$(go run golang.org/x/vuln/cmd/govulncheck@latest -format json ./...)"
    dependencies="$(jq -rs '
        [.[] | select(.finding) | .finding
          | select(.fixed_version != null and .fixed_version != "")
          | .trace[0].module
          | select(. != null and . != "stdlib" and . != "toolchain")]
        | unique[]' <<<"$report")"
    if [[ -z "$dependencies" ]]; then
      # JSON mode reports findings without a failing status; enforce the usual gate.
      go run golang.org/x/vuln/cmd/govulncheck@latest ./...
      exit 0
    fi
    [[ "$attempt" -lt 4 ]] \
      || fail "Fixable vulnerabilities remain after three remediation passes."
    while IFS= read -r dependency; do
      echo "Updating vulnerable module $dependency" >&2
      go get "$dependency@latest" "go@$target" toolchain@none
    done <<<"$dependencies"
    go mod tidy -go="$target"
    go mod edit -toolchain=none
  done
  ;;
*)
  fail "Usage: $0 check | update VERSION | remediate"
  ;;
esac
