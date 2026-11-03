---
title: Install
description: Install the stormbreaker command with Homebrew, an install script from GitHub or a GitHub Action, the orchestrator's, dispatcher's and UI's images, and stormbreaker's packs for axx.
---

Stormbreaker comes in three parts:

- **packs** for axx, which a suite lists;
- **three images**, the orchestrator, the dispatcher and the UI;
- **a command**, `stormbreaker`, which serves all three and works with streams.

## The packs

List them in your suite's `axx-packs.yaml` by module; axx builds itself with them on its first run:

```yaml title="axx-packs.yaml"
packs:
  - rest
  - kafka
  - github.com/nimbusxr/stormbreaker/packs/core@v0.1.0
  - github.com/nimbusxr/stormbreaker/packs/rest@v0.1.0
  - github.com/nimbusxr/stormbreaker/packs/kafka@v0.1.0
  - github.com/nimbusxr/stormbreaker/packs/prometheus@v0.1.0
```

`stormbreaker-core` is always needed: it ties scenarios to runs. List the packs each of the others builds on too: axx's `rest` for stormbreaker-rest, and axx's `kafka` for stormbreaker-kafka.

## The images

- `ghcr.io/nimbusxr/stormbreaker-orchestrator`: one per shared environment ([deploy it](/guides/deploy-the-orchestrator/)).
- `ghcr.io/nimbusxr/stormbreaker-dispatcher`: a dispatcher with no hooks, for an app with nothing to prepare or clean up. Teams with something to do build their own with the [dispatcher-sdk](/guides/write-a-dispatcher/).
- `ghcr.io/nimbusxr/stormbreaker-ui`: optional, beside an environment's orchestrator ([watch runs in it](/guides/watch-in-the-ui/)).

All three run as UID 1001, and under an arbitrary UID in group 0, as OpenShift runs them. All three trust only the public CAs; add yours in a Dockerfile of your own, `FROM` ours.

## The command

With Homebrew, on macOS or Linux:

```sh
brew install nimbusxr/tap/stormbreaker
```

Or with the install script, from stormbreaker's repository on GitHub:

```sh
curl -fsSL https://raw.githubusercontent.com/nimbusxr/stormbreaker/main/scripts/install.sh | sh
```

On Windows, in PowerShell:

```powershell
irm https://raw.githubusercontent.com/nimbusxr/stormbreaker/main/scripts/install.ps1 | iex
```

Both scripts download the newest release from GitHub and check it against its `checksums.txt`.

In GitHub Actions ([run in CI](/guides/run-in-ci/)):

```yaml
- uses: nimbusxr/setup-stormbreaker@v0
```

Or download a release from GitHub. `stormbreaker version` says which you have.
