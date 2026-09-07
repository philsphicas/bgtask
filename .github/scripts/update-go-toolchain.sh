#!/usr/bin/env bash
set -euo pipefail

export GOTOOLCHAIN=local

fail() {
  echo "::error::$*" >&2
  exit 1
}

validate_version() {
  [[ "$1" =~ ^[0-9]+\.[0-9]+(\.[0-9]+)?$ ]] ||
    fail "Expected a released Go version, got '$1'."
}

case "${1:-}" in
  check)
    # Read metadata without invoking Go: the module may require a newer compiler.
    current="$(awk '$1 == "go" { print $2 }' go.mod)"
    toolchain="$(awk '$1 == "toolchain" { print $2 }' go.mod)"
    latest="$(go env GOVERSION)"
    latest="${latest#go}"
    validate_version "$current"
    validate_version "$latest"

    preferred="$current"
    if [[ -n "$toolchain" && "$toolchain" != default ]]; then
      preferred="${toolchain#go}"
      validate_version "$preferred"
    fi

    if [[ "$(printf '%s\n' "$current" "$preferred" "$latest" | sort -V | tail -n1)" != "$latest" ]] ||
       [[ "$current" == "$latest" && -z "$toolchain" ]]; then
      echo "update-available=false"
      exit 0
    fi

    # Classify against the minimum version, not an already-newer preferred toolchain.
    IFS=. read -r current_major current_minor _ <<< "$current"
    IFS=. read -r latest_major latest_minor _ <<< "$latest"
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
    [[ "$installed" == "go$latest" ]] ||
      fail "Expected go$latest, but the installed toolchain is $installed."
    go get "go@$latest" toolchain@none
    go mod tidy -go="$latest"
    # Keep go.mod as the sole version source even if tidy added a toolchain line.
    go mod edit -toolchain=none
    ;;
  *)
    fail "Usage: $0 check | update VERSION"
    ;;
esac
