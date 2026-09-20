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
#
# Tools are fetched as pinned upstream release archives and verified against the
# SHA-256 digests recorded below before anything is unpacked or executed. No
# remote script is ever fed to an interpreter: a repointed upstream install
# script, a hijacked CDN, or a MITM cannot run code here, it can only fail the
# digest check. Refresh VERIFIED_SHA256 whenever a version below changes; the
# digests come from each project's published release checksums file.
set -euo pipefail

# Versions kept in lockstep with .github/workflows/main.yml.
GOLANGCI_LINT_VERSION="v2.11.4"
YAEGI_VERSION="v0.16.1"

# "<tool> <version> <os> <arch>" -> SHA-256 of the release archive.
VERIFIED_SHA256="
golangci-lint v2.11.4 linux amd64  200c5b7503f67b59a6743ccf32133026c174e272b930ee79aa2aa6f37aca7ef1
golangci-lint v2.11.4 linux arm64  3bcfa2e6f3d32b2bf5cd75eaa876447507025e0303698633f722a05331988db4
golangci-lint v2.11.4 darwin amd64 c900d4048db75d1edfd550fd11cf6a9b3008e7caa8e119fcddbc700412d63e60
golangci-lint v2.11.4 darwin arm64 02db2a2dae8b26812e53b0688a6f617e3ef1f489790e829ea22862cf76945675
yaegi v0.16.1 linux amd64  396e30227c21172324147a3c603a044a0cc0ed8bbda490fd34831905ff977f96
yaegi v0.16.1 linux arm64  8bdde9618d063b16c7f9f43a7895935c589e741d7428fcabed1e7870dbb28038
yaegi v0.16.1 darwin amd64 824946d3205b69e7d1aa790d9ba72cb700af9c0381da7f9334696629445cd5b6
yaegi v0.16.1 darwin arm64 8715a544ca6286008f91076fe8f08928ae5cfa5ea497f3f24e0ba5ffb31225f4
"

GOBIN="$(go env GOPATH)/bin"
mkdir -p "$GOBIN"
export PATH="$GOBIN:$PATH"

# One scratch directory for every download, removed however the script exits so
# a failed or interrupted bootstrap never leaves archives behind.
WORKDIR="$(mktemp -d)"
trap 'rm -rf "$WORKDIR"' EXIT

echo "==> Go toolchain: $(go version)"

case "$(uname -s)" in
  Linux) OS="linux" ;;
  Darwin) OS="darwin" ;;
  *) echo "unsupported OS: $(uname -s)" >&2; exit 1 ;;
esac

case "$(uname -m)" in
  x86_64 | amd64) ARCH="amd64" ;;
  aarch64 | arm64) ARCH="arm64" ;;
  *) echo "unsupported architecture: $(uname -m)" >&2; exit 1 ;;
esac

expected_sha256() {
  local tool="$1" version="$2" digest
  digest="$(awk -v t="$tool" -v v="$version" -v o="$OS" -v a="$ARCH" \
    '$1 == t && $2 == v && $3 == o && $4 == a { print $5 }' <<<"$VERIFIED_SHA256")"
  if [ -z "$digest" ]; then
    echo "no pinned SHA-256 for $tool $version on $OS/$ARCH" >&2
    return 1
  fi
  printf '%s' "$digest"
}

# install_release downloads a pinned upstream archive to disk, refuses to unpack
# it unless its SHA-256 matches the pinned digest, then installs one binary from
# it into GOBIN. Download and execution stay separate steps by design.
install_release() {
  local tool="$1" version="$2" url="$3" member="$4" strip="$5" digest unpack archive

  digest="$(expected_sha256 "$tool" "$version")"
  unpack="$WORKDIR/$tool"
  mkdir -p "$unpack"
  archive="$unpack/archive.tar.gz"

  curl --proto '=https' --tlsv1.2 -sSfL --retry 3 -o "$archive" "$url"

  if ! printf '%s  %s\n' "$digest" "$archive" | sha256sum --check --status -; then
    echo "SHA-256 mismatch for $tool $version ($url)" >&2
    echo "  expected: $digest" >&2
    echo "  actual:   $(sha256sum "$archive" | cut -d' ' -f1)" >&2
    return 1
  fi

  tar -xzf "$archive" -C "$unpack" --strip-components="$strip" "$member"
  install -m 0755 "$unpack/$tool" "$GOBIN/$tool"
}

install_golangci_lint() {
  if "$GOBIN/golangci-lint" version 2>/dev/null | grep -q "${GOLANGCI_LINT_VERSION#v}"; then
    echo "==> golangci-lint ${GOLANGCI_LINT_VERSION} already installed"
    return
  fi
  echo "==> Installing golangci-lint ${GOLANGCI_LINT_VERSION} (pinned, SHA-256 verified)"
  local stem="golangci-lint-${GOLANGCI_LINT_VERSION#v}-${OS}-${ARCH}"
  install_release golangci-lint "$GOLANGCI_LINT_VERSION" \
    "https://github.com/golangci/golangci-lint/releases/download/${GOLANGCI_LINT_VERSION}/${stem}.tar.gz" \
    "${stem}/golangci-lint" 1
}

install_yaegi() {
  if "$GOBIN/yaegi" version 2>/dev/null | grep -q "${YAEGI_VERSION#v}"; then
    echo "==> yaegi ${YAEGI_VERSION} already installed"
    return
  fi
  echo "==> Installing yaegi ${YAEGI_VERSION} (pinned, SHA-256 verified)"
  local stem="yaegi_${YAEGI_VERSION}_${OS}_${ARCH}"
  install_release yaegi "$YAEGI_VERSION" \
    "https://github.com/traefik/yaegi/releases/download/${YAEGI_VERSION}/${stem}.tar.gz" \
    yaegi 0
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
