#!/usr/bin/env sh
# SPDX-License-Identifier: Apache-2.0
# Prints the newest release tag (v0.1.0...), or v0.0.0 before the first release, so GoReleaser
# never guesses from `git describe` (docs-v* tags sit on the same commits):
#
#   GORELEASER_CURRENT_TAG=$(scripts/cli-tag.sh) goreleaser release --snapshot --clean
git describe --tags --match 'v[0-9]*' --abbrev=0 2>/dev/null || echo v0.0.0
