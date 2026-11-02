#!/usr/bin/env bash
# SPDX-License-Identifier: Apache-2.0
#
# Which CI jobs a pull request needs, from the files it changes, one per line on stdin. Prints
# name=value lines for $GITHUB_OUTPUT: go, examples, docs and workflows (true or false), and
# codeql, the matrix of the languages CodeQL analyzes.
#   usage: git diff --name-only BASE...HEAD | scripts/ci-changes.sh
#          scripts/ci-changes.sh --all    # every job: pushes to main
#          scripts/ci-changes.sh --none   # no job: the merge queue, on a tree a run passed
# A change to CI itself runs every job too.
set -euo pipefail

go=false examples=false docs=false workflows=false
lang_go=false lang_js=false lang_actions=false
all=false none=false
case "${1:-}" in
  --all) all=true ;;
  --none) none=true ;;
esac

while ! $all && ! $none && IFS= read -r f; do
  [[ -n "$f" ]] || continue
  case "$f" in
    .github/workflows/ci.yml | .github/workflows/codeql.yml | .github/actions/* | scripts/ci-changes.sh | scripts/gotest.sh)
      all=true
      continue
      ;;
  esac
  case "$f" in
    .github/*) workflows=true lang_actions=true ;;
  esac
  # The main Go module: its code, what its tests read, and its build. The examples are a module
  # of their own.
  case "$f" in
    examples/*) ;;
    cmd/* | internal/* | protocol/* | dispatcher/* | streams/* | packs/* | api/* | testdata/* | build/* | scripts/* | \
      go.mod | go.sum | .mise.toml | .golangci.yml | .goreleaser.yaml | *.go)
      go=true ;;
  esac
  case "$f" in
    examples/* | cmd/* | internal/* | protocol/* | dispatcher/* | streams/* | packs/* | api/* | go.mod | go.sum | .mise.toml)
      examples=true ;;
  esac
  case "$f" in
    docs/*) docs=true ;;
  esac
  case "$f" in
    *.go | go.mod | go.sum | */go.mod | */go.sum) lang_go=true ;;
    *.js | *.mjs | *.cjs | *.ts | *.tsx | *.astro | *package.json | *package-lock.json) lang_js=true ;;
  esac
done

if $all; then
  go=true examples=true docs=true workflows=true
  lang_go=true lang_js=true lang_actions=true
fi

langs=""
add() { langs="${langs:+$langs,}{\"language\":\"$1\",\"build-mode\":\"$2\"}"; }
if $lang_go; then add go autobuild; fi
if $lang_js; then add javascript-typescript none; fi
if $lang_actions; then add actions none; fi
# Every pull request gets a code scanning result (OpenSSF Scorecard's SAST check counts each merged
# one without as unchecked): one that changes no language's files analyzes the workflows.
if [[ -z "$langs" ]] && ! $none; then add actions none; fi

echo "go=$go"
echo "examples=$examples"
echo "docs=$docs"
echo "workflows=$workflows"
echo "codeql={\"include\":[$langs]}"
