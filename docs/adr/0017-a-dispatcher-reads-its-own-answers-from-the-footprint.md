# 0017: A dispatcher reads its own answers from the footprint

- Status: Accepted
- Date: 2026-11-01

## Context

A cleanup runs in passes, and a dispatcher can restart between them. It has to know what it has already removed, or promised clean by a date, so that it neither does nor reports it twice, and never mistakes a record it removed for one that never arrived.

The footprint holds every such statement, but a walk of it did not say what each one was. A footprint page's reports, and the settled claims, gave who claimed which identity where, without its resolution. A dispatcher could only remember its answers itself. The lab's tracking dispatcher did, in memory: restarted mid-cleanup, it took the records it had already removed for records that never arrived, and reported them clean-by, over its own removals.

## Decision

- **A footprint page's reports carry their resolution**, and `cleanBy` with clean-by. So do the settled claims (`GET /runs/{id}/footprint/claims`). Each claim carries what its instance last said of it at that place, since a later report from the same instance at the same place supersedes its own earlier one.
- **The dispatcher-sdk's `Claim` carries `Resolution` and `CleanBy`**, and `Run.Unanswered(store, locator)` walks what the dispatcher still has to answer for at a place: the identities it declared there that no instance of it has reported removed-verified or clean-by, each once, in the order first declared. It holds that place's identities while it walks; `Identities`, which holds none, stays for places too large for that.
- **A dispatcher keeps nothing it needs in memory.** What it has done is in the footprint. The lab's dispatchers clean up from `Unanswered`.

The fields are additions: a client that ignores them works as before.

## Alternatives

**A cursor in the dispatcher.** A dispatcher has no storage of its own by design, and a cursor lost with its pod is the memory this removes.

**Answer server-side: a route for what an instance has not answered for.** The orchestrator stores identities packed, one blob a report, and settling them per identity is the walk the dispatcher already makes. It can come later as an optimization, with the same meaning.
