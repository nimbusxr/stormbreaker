# 0016: The orchestrator's store is sized, reserved and pruned

- Status: Accepted
- Date: 2026-11-01

## Context

The orchestrator keeps everything in one SQLite database on one volume ([0010](0010-sqlite-orchestrator.md)). What grows is the footprint. Measured on the scale tests ([scale](https://stormbreaker.nimbusxr.us/explanations/scale/)):

- **A claim takes about 40 bytes on disk.** A run's claims are its identities times the statements made about each one: the suite's declaration, each team's derived declaration, and each removal. A run of 60 million parcels, with one team deriving a record for each, made 180 million claims and took 7.2 GB. A billion parcels took 119 GB.
- **Closed runs are kept for 14 days.** A busy environment's store is the sum of two weeks of runs.
- **A full volume fails writes.** SQLite rolls a failed write back and the store stays consistent, but a run whose declarations fail stops halfway, and a cleanup whose removals fail cannot say what it removed.
- **Pruning deleted a whole run in one transaction**, which would grow the write-ahead log by the size of the run, and gave none of the room back to the volume.

## Decision

- **A closed run's identities are kept for less long than the run.** After the footprint retention (`--footprint-retention`, 72 hours by default, at most `--retention`), a closed run's identities are dropped. Its journal, which counts its claims by place, stays for the retention. Reading the dropped identities answers `410 Gone`; a run's streams can generate them again, record for record.
- **Pruning goes a batch at a time**, 2,000 reports per transaction, and gives the freed pages back to the volume (new databases are made with `auto_vacuum` incremental).
- **The orchestrator keeps a reserve on its volume** (`--disk-reserve`, 1 GiB by default). Below it, new runs and declarations are refused with `507 Insufficient Storage`. Removals, statuses and closing always go through, so cleanup is never what a full disk stops.
- **Its metrics say how full it is.** `GET /metrics`, in Prometheus's text format, gives the store's size and its reusable pages, the volume's free bytes and the reserve, the claims kept, and the runs, holds and notifications. They are counts, with no run's or team's name.
- **Size the volume from the claims.** Bytes are about 40 times the claims a footprint retention holds, plus a little for journals. The guide says how.

**SQLite is enough while one writer keeps up and the footprint fits one volume.** It did for a run of a billion identities, for 2,000 dispatcher instances, and for 10 runs at the same moment. It struggled at 35 or more runs opening at the same moment, which reads beside the writer and coalesced notifications address. (Since then, with narrowed walks ([0024](0024-a-hook-walks-only-what-it-derives-from.md)), checkpoints off the writer's connection and renewals written together ([0025](0025-lease-renewals-are-written-together.md)), a 2-CPU orchestrator held an enterprise's peak hour of 2,400 smoke runs among 6,300 instances, and three times that, with its writer at most 28% busy.) An organization that outgrows it runs many groups at once against one environment, or needs the orchestrator to survive losing its node without waiting for the volume. That is a store on Postgres, and it will be its own ADR. The SQL is in one package, behind `database/sql`.

## Alternatives

**Postgres from the start.** Every environment, a lone team's included, would need a database to run. Most need one small orchestrator, and its volume is backed up like any other.

**Keep identities in object storage.** A footprint read would stream from it. Worth it for runs far past a billion identities; not before.

**Summarize a run's footprint as it closes.** A closed run could no longer be checked against what it claimed. The footprint retention keeps that check for three days by default, and the counts for the retention.

**No reserve.** A full disk would fail runs halfway, and their cleanups with them.
