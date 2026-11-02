# 0023: A cleanup has a deadline

- Status: Accepted
- Date: 2026-11-01

## Context

A run closes when every instance on it has completed its cleanup ([0012](0012-failed-and-abandoned-runs-are-cleaned-up.md)). A cleanup that never verifies kept its run in cleanup for good. The cleanup hold ([0015](0015-runs-never-overlap-on-a-dispatcher.md)) stops such a run from blocking its group, but the run itself stayed open, was never pruned, and said nothing until someone looked.

## Decision

- **A run may stay in cleanup for the cleanup deadline** (`--cleanup-deadline`, 24 hours by default). Past it, the orchestrator closes it **unfinished**.
- **An unfinished run says what it left.** Its journal names the instances that never completed, whose claims nobody answered for; its view and the list of runs show `unfinished`; the metrics count unfinished runs. Its footprint stays for the footprint retention, for whoever cleans up by hand, and its streams can generate its identities again for as long as their definitions are kept.
- **A suite never waits that long.** It fails at its own close timeout, naming the instances that had not completed; the deadline is for the runs nobody is watching, abandoned ones above all.

## Alternatives

**Keep the run open until a person closes it.** Then nothing is ever pruned, and an abandoned run's cleanup failure is a run in cleanup forever, which no alert distinguishes from one still working.

**Close it as completed.** It would hide what was left.
