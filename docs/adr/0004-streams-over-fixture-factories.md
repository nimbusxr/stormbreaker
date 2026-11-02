# 0004: Volume data is streams over the fixture factories

- Status: Accepted
- Date: 2026-11-01

## Context

Load needs volume data that is schema-valid, looks real, never collides, and can be recomputed later, without a second way of describing data beside the one teams already use.

## Decision

A stream (`<name>.stream.yaml`) is a count of keys and a weighted mix of shapes. Each shape is a fixture of axx's fixture factories, or a series of them.

- **Every record is determined.** Record *i* is a pure function of the definition, the seed (the run's id), the run and *i*. A key's shape is chosen by a hash of the stream's name, the seed, the run and the key. Every value comes from the factory's expressions, evaluated for the record.
- **Identities are derived per record.** An identity a fixture leaves out is derived from the record's own discriminator, with the identity's format (an ordinary UUID).
- **Ranges are independent.** Any range of keys can be generated alone, and ranges reassemble into the whole stream.
- **Collisions are caught before traffic.** A lint checks that no two keys share an identity.

## Consequences

- What a run created can be recomputed from its recorded streams and seeds.
- Expression functions are pure, or answer from the pairings lock. Derived identities need none.
