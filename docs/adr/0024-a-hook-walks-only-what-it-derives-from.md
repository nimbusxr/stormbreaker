# 0024: A hook walks only what it derives from

- Status: Accepted
- Date: 2026-11-01

## Context

A hook reads the run's footprint to decide what its team will create, or what it has to remove. Every walk read the whole footprint, and the hook threw away what it did not derive from. Every growth of the footprint also started a round on every instance in the group, and each round walked the whole footprint again.

That cost grew with the run and with the group together. At a billion parcels, a round that found nothing new took 10 min 35 s to walk 2 billion claims, though its hook needed only the suite's billion. In an organization, 500 instances in 25 runs at once spent most of the orchestrator's CPU reading pages that hooks discarded. The gate took 7 s to open, and the process held 1.3 GB.

## Decision

- **A walk names what it reads.** `GET /runs/{id}/footprint` takes `dispatcher` (repeated), `store` and `locator`. The orchestrator matches them on the reports' rows, in the order of the run's reports, and reads a report's identities only when it matches. `until` still bounds the whole footprint, so a walk ends at the same report whether it is narrowed or not.
- **The SDK narrows with `From` and `At`.** `run.Footprint(ctx, sdk.From(sdk.Suite), sdk.At("parcels", "/parcels"))`. `Identities` narrows to its place, and `Unanswered` to the dispatcher's own claims at its place, without being asked.
- **A round is not run again while nothing new is reported where it read.** The runtime records each walk a round's hook made: its filter, and the report it read up to. When the footprint grows, it asks, with one small request a walk, whether any report past that point matches the filter. If none does, the hook would read exactly what it read before. Its declaration is a pure function of what it read and of the run, so it would declare the same. The instance settles on the new version without a round.
- **What a hook may depend on is stated.** A declaration must be a pure function of what the hook walks through its footprint and of the run: its streams, its window, whether it failed or was abandoned. A round settled on other values of those is not reused. A hook that walks nothing is never run again for the run's state it settled in.
- **An unnarrowed walk still works.** The zero filter is the whole footprint, as before. Its round is skipped only when nothing at all was reported past it.

## Consequences

- In the organization test, 25 runs over 500 instances opened their gates in 2.2 s rather than 6.9 s, and closed in 0.5 s rather than 2.1 s. A footprint page took 19 ms rather than 66 ms, the process used 6.5 cores rather than 11.1, and it held 518 MB rather than 1.29 GB. A run of 60 million parcels, its hook walking only the suite's, opened its gate in 1 min 37.5 s rather than 2 min 17.7 s. Each pair was measured in one session, the hooks walking narrowed and then whole.
- A team whose hook derives from the suite's records no longer pays for every other team's declarations, and is no longer woken to re-read them.
- The Go client's `Footprint` and `FootprintPage`, and the runtime's `Footprint` type, take a filter. That is a change to the dispatcher-sdk and the client, before their first release.
- Dispatchers in other languages can skip rounds the same way; the conformance kit does not require it. A dispatcher that runs every round is slower, never wrong.

## Alternatives

**Walk from where the last round stopped.** A hook would see only what is new, and declare from it. The declaration would then depend on what the dispatcher remembers. After a restart, or in another replica, it would differ, and rounds would not converge. The footprint stays the only memory, and a skipped round reads none of it.

**Places as pairs.** A filter of several (store, locator) pairs would let one walk read two places. Two walks do the same, and each is skipped on its own.

**An index on (run, dispatcher, id).** Matching on the reports' rows already skips the identities, which are almost all of the bytes: a run of a billion identities has about a hundred thousand reports. If reports become small and numerous, the index is a migration away.
