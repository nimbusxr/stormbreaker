-- The orchestrator's records. Times are Unix milliseconds, UTC. Identities and payloads are never
-- stored: only who claimed which identities where, and when.

create table run_groups (
  name       text primary key,
  created_at integer not null
);

-- A dispatcher instance's registration doubles as its lease: it renews by registering again.
create table dispatchers (
  name             text not null,
  instance         text not null,
  team             text not null,
  callback_url     text not null,
  secret           text not null,
  created_at       integer not null,
  lease_expires_at integer not null,
  primary key (name, instance)
);

create table group_members (
  group_name text not null,
  dispatcher text not null,
  primary key (group_name, dispatcher)
);

create table runs (
  id                   text primary key,
  group_name           text not null,
  state                text not null,
  initiator            text not null,
  failed               integer not null default 0,
  fail_reason          text,
  abandoned            integer not null default 0,
  opened_at            integer not null,
  window_from          integer not null,
  window_to            integer,
  lease_expires_at     integer not null,
  footprint_updated_at integer,
  -- Counts the times the footprint grew: an instance has seen the footprint when the round it
  -- last settled in began from this version.
  footprint_version    integer not null default 0,
  claims               integer not null default 0,
  -- When the run went to cleanup, and whether it has stopped holding its group: a run holds every
  -- dispatcher of its group from opening until it closes, or until its cleanup outlasts the
  -- cleanup hold (ADR 0015).
  cleanup_at           integer,
  released             integer not null default 0,
  -- When it closed, and whether its identities have been dropped after the footprint retention
  -- (its counts by place stay, in footprint_entries; ADR 0016).
  closed_at            integer,
  footprint_pruned     integer not null default 0,
  -- Whether it closed at the cleanup deadline, some instances never completing (ADR 0023).
  unfinished           integer not null default 0
);
create index runs_state on runs (state);

create table run_streams (
  run_id text not null,
  name   text not null,
  seed   text not null,
  primary key (run_id, name)
);

create table run_dispatchers (
  run_id         text not null,
  name           text not null,
  instance       text not null,
  required       integer not null,
  state          text not null,
  last_status    text,
  evidence       text,
  detail         text,
  last_report_at integer,
  -- The footprint version the instance's last prepared or completed round began from.
  footprint_version integer not null default 0,
  primary key (run_id, name, instance)
);

create table events (
  seq    integer primary key autoincrement,
  id     text not null unique,
  run_id text not null,
  type   text not null,
  at     integer not null
);
create index events_run on events (run_id, seq);

create table deliveries (
  event_seq       integer not null,
  dispatcher      text not null,
  instance        text not null,
  status          text not null,
  attempts        integer not null,
  next_attempt_at integer not null,
  primary key (event_seq, dispatcher, instance)
);
create index deliveries_due on deliveries (status, next_attempt_at);
-- Whether an instance already has a notification on its way, which every event asks of every
-- instance in its group: without it, each asks of every delivery pending in the environment.
create index deliveries_to on deliveries (dispatcher, instance, status);

-- A report goes in as it arrived: one row saying who said what where, and one blob holding the
-- identities, one a line. Nothing is rewritten; the merge happens when the footprint is read.
create table footprint_entries (
  id             integer primary key autoincrement,
  run_id         text not null,
  dispatcher     text not null,
  instance       text not null,
  store          text not null,
  locator        text not null,
  resolution     text not null,
  clean_by       text,
  at             integer not null,
  identity_count integer not null,
  digest         integer not null
);
create index footprint_said on footprint_entries (run_id, dispatcher, instance, store, locator, digest);
create index footprint_run on footprint_entries (run_id, id);

create table footprint_blobs (
  entry_id   integer primary key,
  identities blob not null
);

-- The run's story: append only, never revised.
create table run_log (
  seq    integer primary key autoincrement,
  run_id text not null,
  at     integer not null,
  actor  text not null,
  text   text not null
);
create index run_log_run on run_log (run_id, seq);
