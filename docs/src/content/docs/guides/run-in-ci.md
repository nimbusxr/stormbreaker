---
title: Run in CI
description: Run operational suites from the CI runner against the environment, with the orchestrator's address and token as secrets.
---

Operational suites run on the CI runner, never in the cluster: the runner reaches the services' front doors, their topics, their metrics and the orchestrator directly, with no proxy, port-forward or tunnel. That reach is a requirement of the environment.

```yaml title=".github/workflows/smoke.yml"
jobs:
  smoke:
    runs-on: ubuntu-24.04
    steps:
      - uses: actions/checkout@v5
      - uses: nimbusxr/setup-axx@v0
      - uses: nimbusxr/setup-stormbreaker@v0
      - run: stormbreaker streams lint parcel-lifecycle --root data
        working-directory: acceptance
      - run: axx run
        working-directory: acceptance
        env:
          STORMBREAKER_ORCHESTRATOR_URL: ${{ vars.STAGING_ORCHESTRATOR_URL }}
          STORMBREAKER_TOKEN: ${{ secrets.STAGING_ORCHESTRATOR_TOKEN }}
```

- **Two actions.** [setup-axx](https://github.com/nimbusxr/setup-axx) installs axx, which runs the suite and builds stormbreaker's packs from its `axx-packs.yaml`. [setup-stormbreaker](https://github.com/nimbusxr/setup-stormbreaker) installs the `stormbreaker` command, here to check a stream before the run; a job that only runs a suite can leave it out. Both take a `version`, the newest release by default; give one like `0.1.0` to pin it.
- **Cache axx's builds.** axx builds itself with the suite's packs once per list of packs; keep its cache (`~/.cache/axx`) between runs.
- **The results are axx's.** That means its exit code, JUnit and Cucumber reports, with each run's journal and client view attached.
- **Size the runner for the load.** One runner sends about 20,000 HTTP requests a second for each core, 100,000 or more on a large machine ([scale](/explanations/scale/)); beyond that, a run needs several ([roadmap](/explanations/roadmap/)).
