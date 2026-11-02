# 0009: Notifications are Standard Webhooks

- Status: Accepted
- Date: 2026-11-01

## Decision

Notifications are signed as [Standard Webhooks](https://www.standardwebhooks.com):

- a `webhook-id` (the event's id);
- a `webhook-timestamp`;
- a `webhook-signature` of `v1,` and the base64 HMAC-SHA256 of `<id>.<timestamp>.<body>`, keyed with the instance's `whsec_` secret.

Receivers refuse a timestamp more than five minutes off, and drop repeated ids.

## Consequences

A signature scheme with a published specification and test vectors, which axx's mock pack already verifies. The timestamp prevents replays, which a bare HMAC over the body did not.
