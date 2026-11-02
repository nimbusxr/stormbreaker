# 0011: A dispatcher is defined by its protocol

- Status: Accepted
- Date: 2026-11-01

## Context

Teams write their prepare and cleanup code in the languages their services are in. The first implementation loaded plugins into a runtime by a mechanism Go has no equivalent of.

## Decision

- **A dispatcher is whatever speaks the protocol**: the orchestrator's API, and the signed notifications it sends.
- **The Go dispatcher-sdk is its first implementation.** Teams write two hooks, and the SDK owns the protocol.
- **Other flavors come later**, each in a decision of its own: SDKs in other languages, or a stock image that runs any executable as a hook. All are held to a conformance kit, an axx suite that any dispatcher must pass.
