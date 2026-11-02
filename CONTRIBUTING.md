# Contributing to stormbreaker

Thanks for helping make operational acceptance testing safe and pleasant. This guide covers how
to set up, what we expect in a change, and how releases happen.

## Setup

Install [mise](https://mise.jdx.dev), then from the repository root:

```sh
mise install          # Go, golangci-lint, goreleaser, lefthook, gitleaks (+ node for the docs)
lefthook install      # pre-commit formatting/lint, pre-push short tests
mise run test         # go test -race ./...
go build -o bin/stormbreaker ./cmd/stormbreaker
```

## Making a change

1. **Open an issue first** for anything bigger than a bug fix, so we can agree on the approach.
   Changes to a public contract (step text, the orchestrator's API, the notification contract,
   the dispatcher SDK, CLI output) need an ADR in `docs/adr/`.
2. **Keep step text stable.** Gherkin step expressions are public API. Adding steps is fine;
   changing or removing one needs a deprecation that ships a replacement first.
3. **Tests are required.** Unit tests for logic, and an example scenario for new steps.
4. **Generated files are committed and checked.** Run `mise run generate` after touching what
   they are generated from; CI fails on drift.
5. **Conventional commits.** PRs are squash-merged and the PR title becomes the commit message,
   e.g. `feat(orchestrator): …` or `fix(rest): …`. Allowed types: `feat`, `fix`, `perf`,
   `refactor`, `docs`, `test`, `build`, `ci`, `chore`; add `!` for breaking changes.

## Developer Certificate of Origin

Every commit must be signed off (`git commit -s`), certifying the
[Developer Certificate of Origin](https://developercertificate.org/). This applies equally to
changes written with AI assistance: a human contributor reviews the change and signs off, and
the PR template asks you to tick "AI-assisted" when it applies.

## Releases

Releases are automated with release-please: merged conventional commits accumulate in a
release PR, and merging that PR tags the version and publishes binaries and images.
While stormbreaker is `0.x`, breaking changes bump the minor version.

Nothing is published (releases, images, the Homebrew cask, the nightly build, the docs site,
Scorecard results) unless the `STORMBREAKER_PUBLISH` repository variable is `true`. Until then,
every run of `release.yml` is a dry run. To check a release locally:

```sh
GORELEASER_CURRENT_TAG=$(scripts/cli-tag.sh) goreleaser release --snapshot --clean --skip=sign,sbom
scripts/release-check.sh
```
