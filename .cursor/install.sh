#!/usr/bin/env bash
# Idempotent Cloud Agent bootstrap for the traefik-laravel-forge plugin.
#
# The base image already provides Go 1.22 (the version pinned in go.mod and both
# CI workflows). This script adds the two extra tools CI uses and primes caches:
#   - golangci-lint  (make lint)
#   - yaegi          (make yaegi_test — the real Traefik plugin runtime)
#
# Both tools install into $(go env GOPATH)/bin, which CI puts on PATH (actions/
# setup-go does this automatically) but a fresh Cloud Agent shell does not. The
# project's Makefile invokes `golangci-lint` and `yaegi` by bare name, so this
# script makes them reachable on the default interactive PATH — otherwise
# `make` / `make yaegi_test` fail with "command not found" on a fresh agent.
set -euo pipefail

# Versions kept in lockstep with .github/workflows/main.yml.
GOLANGCI_LINT_VERSION="v2.11.4"
YAEGI_VERSION="v0.16.1"

GOBIN="$(go env GOPATH)/bin"
mkdir -p "$GOBIN"
export PATH="$GOBIN:$PATH"

echo "==> Go toolchain: $(go version)"

install_golangci_lint() {
  if "$GOBIN/golangci-lint" version 2>/dev/null | grep -q "${GOLANGCI_LINT_VERSION#v}"; then
    echo "==> golangci-lint ${GOLANGCI_LINT_VERSION} already installed"
    return
  fi
  echo "==> Installing golangci-lint ${GOLANGCI_LINT_VERSION}"
  curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh |
    sh -s -- -b "$GOBIN" "${GOLANGCI_LINT_VERSION}"
}

install_yaegi() {
  if "$GOBIN/yaegi" version 2>/dev/null | grep -q "${YAEGI_VERSION#v}"; then
    echo "==> yaegi ${YAEGI_VERSION} already installed"
    return
  fi
  echo "==> Installing yaegi ${YAEGI_VERSION}"
  curl -sfL https://raw.githubusercontent.com/traefik/yaegi/master/install.sh |
    bash -s -- -b "$GOBIN" "${YAEGI_VERSION}"
}

# expose_on_path makes a GOBIN tool reachable on the default shell PATH. It
# prefers a symlink in /usr/local/bin (already on PATH for every shell,
# independent of profile files); if that directory is not writable and no
# passwordless sudo exists, it falls back to appending GOBIN to the shell
# profiles so future interactive shells pick it up.
expose_on_path() {
  local name="$1"
  local target="$GOBIN/$name"
  local link="/usr/local/bin/$name"

  if [ -w /usr/local/bin ]; then
    ln -sf "$target" "$link"
    return
  fi
  if command -v sudo >/dev/null 2>&1 && sudo -n true 2>/dev/null; then
    sudo ln -sf "$target" "$link"
    return
  fi

  # Fallback: ensure GOBIN is on PATH for future interactive shells.
  local line="export PATH=\"\$PATH:$GOBIN\""
  for profile in "$HOME/.bashrc" "$HOME/.profile"; do
    if [ -f "$profile" ] && ! grep -qF "$line" "$profile"; then
      printf '\n# Added by traefik-laravel-forge .cursor/install.sh\n%s\n' "$line" >>"$profile"
    fi
  done
}

install_golangci_lint
install_yaegi

echo "==> Exposing tools on PATH"
expose_on_path golangci-lint
expose_on_path yaegi

echo "==> Downloading Go modules"
go mod download

echo "==> Priming build/test cache"
CGO_ENABLED=0 go build ./...

echo "==> Bootstrap complete"
echo "    golangci-lint: $("$GOBIN/golangci-lint" version 2>&1 | head -1)"
echo "    yaegi:         v$("$GOBIN/yaegi" version 2>&1 | head -1)"
echo "    resolved golangci-lint -> $(command -v golangci-lint || echo 'NOT ON PATH')"
echo "    resolved yaegi         -> $(command -v yaegi || echo 'NOT ON PATH')"
