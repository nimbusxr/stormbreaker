# 0019: Each team has its own credentials

- Status: Accepted
- Date: 2026-11-01

## Context

The orchestrator has one bearer token. Whoever holds it can do anything:

- register as any dispatcher, and so replace another team's callback URL and secret: on the lab, registering `parcels` as another team took it over;
- report another team's instance prepared, or its data removed;
- open, publish to or abandon any group's runs.

In an organization, the token sits in every team's dispatcher and every suite's CI. One leak, or one mistake, reaches every team.

## Decision

- **Principals.** The orchestrator reads its principals from a file (`--principals`, mounted from a secret). Each has a name, the SHA-256 of its token (never the token), and what it may do:
  - `admin`: everything, groups included. The platform team's.
  - `suites`: the groups it may open runs in, and act as the suite of: declare, publish, heartbeat. A team's CI.
  - `dispatchers`: the dispatchers it may register, join to groups, and report for. A team's dispatchers.
- **Reads are open to every principal.** Runs, journals, the footprint and the event feed are the environment's shared record; a dispatcher reads runs it does not take part in, to know it is not in them.
- **A request outside its principal's grants is refused with `403`**, naming what it lacks. A request without a known token is `401`, as before.
- **A dispatcher's name belongs to one team.** A registration of a name whose live instances belong to another team is refused with `409`, whoever asks.
- **`--token` stays**, as one admin principal: the lab's, and a lone team's, before there are teams to tell apart.

## Alternatives

**mTLS.** Strong, and the platform team's to run: a certificate authority, rotation, and a mesh or ingress that passes the client's identity through. Tokens in a secret work everywhere, and a mesh can still add mTLS in front.

**OIDC tokens from the CI and the cluster.** The right end state for many organizations, and it needs an issuer the orchestrator trusts, which differs per organization. A principals file maps any of them later: a principal is what a credential may do, not how it was issued.

**One token per group.** Suites and dispatchers would share it, so a dispatcher could abandon a run, and a suite could report a team's cleanup done.
