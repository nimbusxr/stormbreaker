# 0021: Dispatchers say who they are, and what they speak

- Status: Accepted
- Date: 2026-11-01

## Context

Two things a dispatcher used to leave unsaid went wrong in ways that looked like something else.

- **Its instance was named for its pod by default.** A run counts on the instances it opened with. A rollout mid-run replaced a pod with one of another name, which was never on the run: the run failed at its gate, and the region that pod answered for kept its data. On the lab, a rollout 15 s into the traffic failed the run 21 s later, and left 1,500 parcels behind.
- **Nobody said which protocol they spoke.** A dispatcher from before footprint versions ([0013](0013-rounds-settle-on-a-footprint-version.md)) would report rounds without a version, and hold every gate until the readiness timeout. Nothing said why.

## Decision

- **An instance has a stable name, and the dispatcher-sdk requires it.** `STORMBREAKER_INSTANCE` names what the instance answers for: its region or its partition. A dispatcher refuses to start without it, and says why. Replicas of one instance are not instances; a pod that replaces another takes its name.
- **The protocol has a version** (`protocol.Version`, 1). A dispatcher registers, and a suite opens a run, saying the version it speaks. The orchestrator refuses a version it does not speak, or none, with `422` and what to upgrade: the dispatcher-sdk, the stormbreaker packs, or the orchestrator itself. Its health check says the version it speaks.
- **The version goes up only for a change that would make an older party misbehave rather than fail.** Additions an older party ignores leave it where it is.

## Alternatives

**Keep the pod's name, and move a run's claims to whichever pod comes next.** The orchestrator cannot tell a pod that replaced another from a new instance, and guessing wrong hands one region's cleanup to another.

**Version the API's paths.** A path says how a request is shaped, not how a dispatcher behaves; the mismatch that mattered was in behavior.

**Negotiate a version per request.** Every party speaks one version at a time; saying it once, when it registers or opens a run, is enough.
