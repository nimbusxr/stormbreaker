-- The final progress of a run's suite (ADR 0027): how its scenario went, as JSON. The live
-- progress before it is held in memory and never written.
alter table runs add column progress text;
