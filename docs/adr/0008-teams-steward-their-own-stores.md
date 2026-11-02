# 0008: Teams steward their own stores

- Status: Accepted
- Date: 2026-11-01

## Decision

- **Every footprint entry ends one of two ways**: removed and verified (removed, then checked gone), or clean by a date.
- **Teams decide how, for their own stores.** The docs give common answers, not rules: retention for topics, tombstones for compacted topics, TTLs for caches, delete-by-query for indices, rebuilding or declaring an aggregate uncorrectable.
- **Teams that take no part are never asked to do anything.** Their dependencies are mocked around.
