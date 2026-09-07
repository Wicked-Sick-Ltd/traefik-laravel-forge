#!/usr/bin/env bash
# Idempotent Cloud Agent bootstrap for the traefik-laravel-forge plugin.
#
# The base image already provides Go 1.22 (the version pinned in go.mod and both
# CI workflows) with $(go env GOPATH)/bin on PATH. This script adds the two extra
# tools CI uses and primes the caches:
#   - golangci-lint  (make lint)
#   - yaegi          (make yaegi_test — the real Traefik plugin runtime)
set -euo pipefail

# Versions kept in lockstep with .github/workflows/main.yml.
GOLANGCI_LINT_VERSION="v2.11.4"
YAEGI_VERSION="v0.16.1"

GOBIN="$(go env GOPATH)/bin"
mkdir -p "$GOBIN"

echo "==> Go toolchain: $(go version)"

install_golangci_lint() {
  if command -v golangci-lint >/dev/null 2>&1 &&
     golangci-lint version 2>&1 | grep -q "${GOLANGCI_LINT_VERSION#v}"; then
    echo "==> golangci-lint ${GOLANGCI_LINT_VERSION} already installed"
    return
  fi
  echo "==> Installing golangci-lint ${GOLANGCI_LINT_VERSION}"
  curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh |
    sh -s -- -b "$GOBIN" "${GOLANGCI_LINT_VERSION}"
}

install_yaegi() {
  if command -v yaegi >/dev/null 2>&1 &&
     yaegi version 2>&1 | grep -q "${YAEGI_VERSION#v}"; then
    echo "==> yaegi ${YAEGI_VERSION} already installed"
    return
  fi
  echo "==> Installing yaegi ${YAEGI_VERSION}"
  curl -sfL https://raw.githubusercontent.com/traefik/yaegi/master/install.sh |
    bash -s -- -b "$GOBIN" "${YAEGI_VERSION}"
}

install_golangci_lint
install_yaegi

echo "==> Downloading Go modules"
go mod download

echo "==> Priming build/test cache"
CGO_ENABLED=0 go build ./...

echo "==> Bootstrap complete"
echo "    golangci-lint: $(golangci-lint version 2>&1 | head -1)"
echo "    yaegi:         $(yaegi version 2>&1 | head -1)"
