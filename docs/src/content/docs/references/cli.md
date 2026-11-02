---
title: Command line
description: "The stormbreaker command: the orchestrator, a dispatcher with no hooks, groups, and streams."
---

| Command | What it does |
|---|---|
| `stormbreaker orchestrator serve` | serves the orchestrator's API and sends its notifications, and prints its URL ([settings](/guides/deploy-the-orchestrator/#settings)) |
| `stormbreaker ui serve` | serves the UI for one or more environments' orchestrators, reading through their API, and prints the link to open ([watch it in the UI](/guides/watch-in-the-ui/)) |
| `stormbreaker dispatcher serve` | serves a dispatcher with no hooks: it takes part, and reports prepared, then completed ([settings](/guides/write-a-dispatcher/#run-it)) |
| `stormbreaker groups` | lists an orchestrator's groups and their dispatchers (`--url`, `--token`, or `STORMBREAKER_ORCHESTRATOR_URL` and `STORMBREAKER_TOKEN`) |
| `stormbreaker runs` | lists an orchestrator's runs, newest first: their group, state, whether they hold their group, claims and opening (`--state`, `--group`, `--limit` of 50 by default, `--url`, `--token`) |
| `stormbreaker groups create <name>` | makes a group, for a team that runs before it has a dispatcher |
| `stormbreaker streams lint <stream>` | generates a stream and checks that no two keys claim the same identity |
| `stormbreaker streams generate <stream> --run <id>` | writes a run's records, one JSON object a line (`--from`, `--to` for a range of keys) |
| `stormbreaker streams ids <stream> --run <id>` | writes the identities a run's stream created, one a line |
| `stormbreaker version` | the version |

The `streams` commands take `--root`, the directory of the fixture factories and the streams. Those that generate a run's records also take `--seed` (the run's id by default) and `--window`, when the run's window opened. `version` and `streams lint` write JSON with `--json`; `streams generate` writes JSON lines, and `groups` and `runs` print tables.
