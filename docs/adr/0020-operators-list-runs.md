# 0020: Operators list runs

- Status: Accepted
- Date: 2026-11-01

## Context

An operator could read one run by its id, and the event feed, and nothing else. Which runs are open, which group holds what, which cleanups are still going: none of that had an answer short of reading the feed from the start. The metrics ([0016](0016-the-orchestrators-store-is-sized-reserved-and-pruned.md)) count runs, without saying which.

## Decision

- **`GET /runs` lists runs, newest first**: each one's group, state, whether it failed or was abandoned, whether it holds its group ([0015](0015-runs-never-overlap-on-a-dispatcher.md)), its claims, and when it opened, went to cleanup and closed.
- **It narrows by state (any of several) and group**, and pages by opening time: a page's `before` asks for older runs.
- **Every principal may read it** ([0019](0019-each-team-has-its-own-credentials.md)), as every run's view and journal.
- **`stormbreaker runs`** prints it.

## Alternatives

**Labels on the metrics with each run's id.** A metric per run grows without bound, and a list answers the question an operator actually asks: which ones.

**Search over the event feed.** The feed is for dispatchers catching up; it says what happened, not where each run is now.
