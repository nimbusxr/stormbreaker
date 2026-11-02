---
name: stormbreaker-dispatchers
description: Write a team's stormbreaker dispatcher with the Go dispatcher-sdk (Prepare and Cleanup hooks over a run's footprint). Use when a team's service must take part in shared-environment runs, preparing before the traffic and cleaning up what its systems created.
license: Apache-2.0
---

# Writing a stormbreaker dispatcher

A dispatcher is a team's part in runs: one per app per environment. The dispatcher-sdk (`github.com/nimbusxr/stormbreaker/dispatcher/sdk`) owns the protocol. You write two hooks and call `sdk.Main(sdk.Hooks{Prepare: ..., Cleanup: ...})`.

## Prepare: `func(ctx, run *sdk.Run, declare func(sdk.Entry) error) error`

- Read only what you derive from, a page at a time: `run.Footprint(ctx, sdk.From(sdk.Suite), sdk.At(store, locator))` (narrowed; `run.Footprint(ctx)` is every claim) or `run.Identities(ctx, store, locator)` (one place; an identity can come more than once).
- Call `declare(sdk.Entry{Store, Locator, Identities})` for what your systems will create, as you go: the SDK sends it in chunks. Never collect everything first.
- **The declaration is a pure function of what the hook walks and of the run (streams, window), in the same order each round.** Never mint new identities, and never read the footprint any other way. Rounds run until one adds nothing the orchestrator has not heard; a settled round is not run again until something new is reported where it walked.
- Writes are allowed only if idempotent (upserts), for identities already in the footprint.

## Cleanup: `func(ctx, run *sdk.Run, remove func(sdk.Removal) error) (complete bool, err error)`

- Walk `run.Unanswered(ctx, store, locator)`: what you declared there and have not yet reported removed or clean-by, each once. Keep nothing in memory between passes: a restarted dispatcher reads what it already did from the footprint.
- Remove what your systems created, **check it is gone**, then call `remove(sdk.Removal{Store, Locator, Identities})`, as you go.
- Where removal is impossible now, report `CleanBy: "YYYY-MM-DD"`.
- Return `true` when nothing is left. A store written asynchronously can still receive a run's records: return `false` while records you declared have not appeared, up to a grace period after the run's traffic ended. The hook runs again on each pass, so it must be idempotent.
- Never touch another team's data.

## Errors

Wrap with `sdk.ErrBlocked` when a hook cannot succeed (bad credentials, a missing table): a run requiring you fails at once. Other errors are retried on the next pass.

## Deploy

Environment, required: `STORMBREAKER_DISPATCHER`, `STORMBREAKER_INSTANCE` (a stable name such as the region, never the pod's), `STORMBREAKER_TEAM`, `STORMBREAKER_CALLBACK_URL` (its address + `/events`), `STORMBREAKER_ORCHESTRATOR`. `STORMBREAKER_GROUPS` is how it hears of runs. Optional: `STORMBREAKER_TOKEN` (when the orchestrator asks for one), `STORMBREAKER_SECRET`, `STORMBREAKER_ADDR` (`:8090`), `STORMBREAKER_REASSERT` (`10s`).

Examples: https://github.com/nimbusxr/stormbreaker/tree/main/examples/dispatchers
