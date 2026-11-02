# 0010: The orchestrator keeps its records in SQLite

- Status: Accepted
- Date: 2026-11-01

## Decision

- **The orchestrator's records are a SQLite database** (pure Go, `modernc.org/sqlite`) on a volume of its own: one writer connection, and a pool of readers beside it.
- **It stores identities and where they are, never payloads.** A report is one row and one blob, and nothing is rewritten. The footprint is read a page at a time.
- **One replica**, with the `Recreate` strategy. The SQL stays portable (`returning`, `on conflict`) so that Postgres can come later.

## Durability

What the orchestrator guarantees, and what it assumes of its volume:

- **Every write is one transaction, committed before it is answered or notified.** A transition and the notifications it queues commit together; a footprint report, its claims count and its blob commit together. Nothing is half written.
- **A commit is on the disk before it returns** (`synchronous = FULL`, in WAL mode). What the orchestrator answered for, or told a dispatcher of, survives a crash of the process or of the node. A small write costs about 46 µs instead of 24 µs on an SSD; on network block storage the difference is the fsync's latency.
- **One writer.** The writer is one connection, and the orchestrator runs one replica (`Recreate`). Readers see the last commit and never wait for it. Give the volume `ReadWriteOncePod` where the cluster supports it, so two pods can never open the database at once.
- **The volume must honor fsync**: a block volume, not a network file system that acknowledges writes it has not stored (NFS, SMB). SQLite's guarantees are the volume's.
- **The write-ahead log stays bounded.** Housekeeping checkpoints once a second without waiting for readers, and past 64 MB waits for the readers on older snapshots and starts the log again.
- **Space freed by pruning is returned** (`auto_vacuum = INCREMENTAL`, set when the database is created).

Evidence: a scale test kills an orchestrator with SIGKILL at random moments under concurrent load (`TestKillNineLosesNothingAcknowledged`). Over 40 rounds and 62,008 acknowledged footprint reports, the database passed `integrity_check` every time, no acknowledged claim was lost, every run's claims count matched its footprint, and no acknowledged transition was undone. Another fills the volume (`TestADiskThatFillsLosesNothing`): a write that does not fit fails and rolls back, reads go on, a SIGKILL while full loses nothing, and writes resume once there is room.
