#!/usr/bin/env bash
#
# Publish the sdk/go module to the public mirror repo
# github.com/arcaresearch/arca-go-sdk.
#
# The Go SDK lives in this private monorepo at sdk/go, but its module path is
# github.com/arcaresearch/arca-go-sdk, so it is consumed from a dedicated public
# repo (like the TypeScript SDK's arca-typescript-sdk). This script does a
# history-preserving `git subtree split` of sdk/go and force-pushes it to that
# public repo's `main`, optionally tagging a release.
#
# Usage:
#   ./sdk/go/scripts/publish.sh            # sync main only
#   ./sdk/go/scripts/publish.sh v0.2.0     # sync main AND tag v0.2.0
#
# Requirements:
#   - Run from anywhere inside the monorepo, on an up-to-date `main`.
#   - Push access to arcaresearch/arca-go-sdk via your normal git credentials
#     (no stored secret needed). For CI, see .github/workflows/sync-go-sdk.yml.
#
# Consumers then install with:
#   go get github.com/arcaresearch/arca-go-sdk@<version>
set -euo pipefail

VERSION="${1:-}"
REMOTE="git@github.com:arcaresearch/arca-go-sdk.git"

ROOT="$(git rev-parse --show-toplevel)"
cd "$ROOT"

if [[ -n "$VERSION" && ! "$VERSION" =~ ^v[0-9]+\.[0-9]+\.[0-9]+(-[0-9A-Za-z.-]+)?$ ]]; then
  echo "error: version must be semver-prefixed, e.g. v0.2.0 (got '$VERSION')" >&2
  exit 1
fi

TMP_BRANCH="go-sdk-publish-$$"
cleanup() { git branch -D "$TMP_BRANCH" >/dev/null 2>&1 || true; }
trap cleanup EXIT

echo "Splitting sdk/go subtree (walks full history; this can take ~1 min)..."
git subtree split --prefix=sdk/go -b "$TMP_BRANCH" >/dev/null

echo "Force-pushing subtree to $REMOTE main..."
git push --force "$REMOTE" "$TMP_BRANCH:main"

if [[ -n "$VERSION" ]]; then
  echo "Tagging $VERSION on the public repo..."
  git push "$REMOTE" "$TMP_BRANCH:refs/tags/$VERSION"
fi

echo "Done. Install with: go get github.com/arcaresearch/arca-go-sdk@${VERSION:-latest}"
