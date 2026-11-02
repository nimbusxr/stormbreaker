---
title: Mock partners
description: Stand in for a dependency with axx's WireMock image and a data-only mock image. Its stubs answer what they match, and everything else falls through to the real one.
---

Some services call dependencies that do not take part in a run: a carrier's API, another team's service. Load must never reach them, and a flaky sandbox must never decide a verdict. Stand them in with a mock.

**A mock is a proxy too.** Its stubs answer the calls they match. Everything else falls through to the real dependency, so traffic outside your tests goes where it always went. Point the URL at the mock once, and leave it there.

Stormbreaker has no mock server of its own. It uses axx's: `ghcr.io/nimbusxr/axx-wiremock`, WireMock with axx's OpenAPI validation. Every call to a stub with a spec is checked against the partner's contract: give the partner's OpenAPI document with `OPENAPI_SPEC_SOURCE`, or per stub with `openApiSpecSource`.

## The mock image

A mock's behavior is data: WireMock mappings, committed, in an image that holds nothing else.

```dockerfile title="mocks/courier/Dockerfile"
FROM scratch
COPY default /default
COPY slow /slow
COPY openapi /openapi   # the partner's contract, which every call is checked against
```

```json title="mocks/courier/default/mappings/collection.json"
{
  "request": {"method": "GET", "urlPathPattern": "/courier/collections/.*"},
  "response": {"status": 200, "jsonBody": {"slot": "09:00-12:00"}}
}
```

Push it to a registry like any image. Pinned by its digest, the mock is the same everywhere it runs.

## Deploy it

Mount the mock image read-only as an image volume and point the WireMock image at a profile, a folder of it:

```yaml
containers:
  - name: wiremock
    image: ghcr.io/nimbusxr/axx-wiremock:latest
    args:
      - --root-dir
      - /mocks/default
      - --proxy-all=https://api.courier.example   # the real dependency
      - --max-request-journal-entries=10000
    env: [{name: OPENAPI_SPEC_SOURCE, value: /mocks/openapi/courier.yaml}]   # the partner's contract
    volumeMounts: [{name: mocks, mountPath: /mocks, readOnly: true}]
volumes:
  - name: mocks
    image: {reference: registry.example.com/courier-mock@sha256:..., pullPolicy: IfNotPresent}
```

Where image volumes are not available, an init container copies the mock image into an `emptyDir`; the lab does that (`examples/lab/k8s/courier.yaml`). Run one mock per partner per environment, inside the cluster only: it is never exposed.

## Pass everything else through

`--proxy-all` is all it takes. It adds a catch-all at a lower priority than any stub, so WireMock tries your stubs first, and a call that matches none falls through:

| A call that | Gets |
|---|---|
| matches a stub | the stub's answer; the real dependency never sees it |
| matches no stub | the real dependency's answer |

The URL is in the deployment, so the same mock image works in every environment. A mock passes on HTTP and HTTPS. Events go another way: [publish to a topic of your own](/guides/roll-it-out/).

Three things follow:

- **Stub every call your tests make**, unless you mean it to reach the real dependency. Whatever no stub matches gets there, load included. A stub answers whoever matches it, so keep each as narrow as your tests allow. After a first run at a low rate, list what fell through, from inside the cluster:

  ```sh
  curl -s http://courier:8080/__admin/requests \
    | jq '[.requests[] | select(.responseDefinition.proxyBaseUrl) | .request.url]'
  ```

- **The contract is checked on what passes through too.** When the real dependency breaks its contract, you see it. In the default `fail` mode, the caller gets an HTTP 500 listing the findings. With `OPENAPI_VALIDATION_MODE: report`, the caller gets the real answer, and the finding goes on the mock's journal, where axx fails the run of any scenario that registers the mock. A path the spec does not list counts as a break as well, so give the whole contract, or use `report`.
- **The journal sees all of it.** WireMock keeps each request it serves, headers, bodies and credentials included. `--max-request-journal-entries` keeps a mock that stays in place from growing without end, and its admin API stays inside the cluster.

## Rehearse a bad day

A profile is just a folder: `slow/` with WireMock's `delayDistribution`, `outage/` answering 503. Change behavior by deploying with another `--root-dir`, never by changing a running mock. A profile changes only the calls its stubs match, and everything else still falls through. Redeploying interrupts all that passes through the mock, though, so it is a decision for the environment.
