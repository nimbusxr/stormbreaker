# 0013: Rounds settle on a footprint version, and the footprint streams

- Status: Accepted
- Date: 2026-11-01

## Context

The readiness gate asks whether every required instance has seen everything there is to derive from and found nothing to add ([0003](0003-the-footprint-comes-from-declarations.md)). It used to ask that by time: had the instance reported since the footprint last grew?

At sixty million identities, a round is a walk of them all. That takes tens of seconds, and it breaks the time rule in three ways.

- **A late report passes for a fresh one.** A round begins, the footprint grows while it walks, and it reports after the growth. It never saw what grew, but it reported after it, so the gate counts it.
- **Repeating costs as much as saying.** Every sweep ran the round again. The orchestrator stored every report again, so a settled instance wrote its whole declaration every ten seconds.
- **Cleanup fed itself.** A removal counted as growth, so one team's cleanup reports made every other instance report again, and run its cleanup again to do it.

## Decision

- **The footprint has a version.** It goes up each time a report declares something its reporter has not said at that place before. The run shows it as `footprintVersion`.
- **A round reports the version it began from.** It reads the run, works from the footprint, then reports `prepared` or `completed` with the `footprintVersion` it read. The gate counts an instance only when that is the footprint's version now, and so does close. `in-progress`, `partial` and `blocked` leave what the instance has seen unchanged, and a version the footprint has not reached is refused.
- **A removal is not growth.** It is recorded, but it answers for something and nobody derives from it, so it raises no version and notifies no one.
- **The same statement is recorded once.** A report is skipped when its reporter's latest report at that place, with the same identities, has the same resolution and clean-by date.
- **The dispatcher remembers a settled round.** Until the footprint's version moves past the one a settled round began from, the dispatcher reports that round's status again instead of running another. The report is one request, and it puts back an instance the orchestrator stopped counting on.
- **A long round says so.** While a hook runs, the dispatcher reports `in-progress` every `Reassert`, so an instance working through millions of claims is not taken for one that acknowledged the run and went quiet.
- **The footprint streams.**
  - A page is shaped like the reports it holds, capped near 100,000 identities.
  - A walk passes the first page's `until` to every later page, so it sees the footprint as it was when it began, and never its own reports.
  - Hooks read and report as they go: the dispatcher-sdk hands identities over one at a time and sends what a hook says in chunks of 25,000.

## Alternatives

**Keep the time rule and add a settle delay.** That is a guess, and load makes it wrong in the wrong direction.

**Make the round report the `until` of its walk.** A hook may walk the footprint more than once, or not at all. The run's version, read before the round, holds for any hook.

**Count growth by time (`footprintUpdatedAt`).** Two reports can land in the same millisecond. A counter cannot tie.
