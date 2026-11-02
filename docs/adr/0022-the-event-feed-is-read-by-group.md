# 0022: The event feed is read by group

- Status: Accepted
- Date: 2026-11-01

## Context

The event feed is the backstop for notifications that never arrived ([0009](0009-standard-webhooks.md)). It held every group's events, and a dispatcher that started read it from the beginning, a thousand events a sweep, looking at every run in it: in a busy environment, minutes of history it could do nothing with, before it reached the run it was working on.

## Decision

- **A dispatcher reads its groups' events only.** `GET /events` takes `group`, repeated; the dispatcher passes the groups it joins.
- **Read from the start, a groups' feed begins at their unfinished runs.** What closed before the dispatcher started is nothing it can act on.
- **The cursor never skips.** A page and the feed's end are read in one snapshot, and one writer numbers events as they commit, so a short page moves the cursor past the other groups' events without missing one of its own.
- **Without `group`, the feed is every group's**, as before, for a dispatcher that joins none and for operators.

## Alternatives

**A feed per dispatcher, of the runs it is on.** A dispatcher must also hear of runs opened after it started, in groups it joined; the group is what it listens to.

**Start a fresh dispatcher at the feed's end.** It would miss the runs that were open when it restarted, which are the ones it most needs to finish.

A dispatcher joined to a group by an operator, rather than by its own configuration, still hears that group's runs by notification; its backstop covers the groups it was started with.
