# 0025: Lease renewals are written together

- Status: Accepted
- Date: 2026-11-01

## Context

Every dispatcher instance renews its lease every 10 s, by registering again, and joins its groups again every sixth renewal. Each renewal and each join was a transaction of its own, on the disk before it was answered. In an enterprise's staging environment, with 6,000 instances, that is about 700 commits a second while no run is open. On a Linux volume where a commit costs a third of a millisecond, the orchestrator's one writer would be busy a quarter of the time doing nothing but renewing leases, before any team's run.

A lease's end does not need to be on the disk before the renewal is answered. Nothing is told of a renewal, and an orchestrator that restarts already gives every instance one lease to renew in before any silence counts against it ([0017](0017-a-dispatcher-reads-its-own-answers-from-the-footprint.md)).

## Decision

- **A renewal that changes nothing but the lease is held in memory.** It is from the same team, with the same callback and secret, for a registration whose written lease has not ended. Anything else is written at once, as before: a first registration, a new callback or secret, a lease that ran out, or another team registering the name.
- **Held renewals are written together**, in one transaction, once a second.
- **The sweep that marks lapsed instances unreachable writes them first**, so no instance is taken for gone on a lease the database has not caught up with. Opening a run and joining a group read the written leases, which trail by a second at most against a lease's 20 s of slack after a renewal. The dispatcher listing reads the held renewals too.
- **Joining a group the dispatcher is in, with a live instance, writes nothing.** Neither does the notification loop when nothing is due: it asks a reader first.
- **A crash loses at most a second of renewals.** The restart's grace covers them.

## Consequences

- With 6,300 instances registered and no run open, the orchestrator writes two or three times a second instead of about 700 times, and its writer is busy 1% of the time ([scale](https://stormbreaker.nimbusxr.us/explanations/scale/)).
- A lease reads as renewed at once: `POST /dispatchers` answers with the new end, and `GET /dispatchers` shows it.
- The renewals of an instance that stops are written within a second. Its lease then lapses as before, a lease after its last renewal.

## Alternatives

**Longer leases, renewed less often.** Fewer writes, but an instance that scaled away would hold its runs up for longer before it counted as gone.

**Leases in memory only.** A restarted orchestrator would know no instance as live until each renewed. The grace covers a short restart, not a store that forgets every registration.
