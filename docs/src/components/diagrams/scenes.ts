/**
 * The diagrams' scenes (src/lib/ascii-diagram.ts), each laid out on a grid of characters and
 * told in the steps a reader clicks through.
 */
import type { Scene } from '../../lib/ascii-diagram';

/** A run through an environment, phase by phase. */
export const runTopology: Scene = {
	width: 100,
	height: 30,
	boxes: [
		{ id: 'env', x: 34, y: 0, w: 66, h: 30, title: 'the shared environment', style: 'zone' },
		{ id: 'runner', x: 0, y: 4, w: 30, h: 13, title: 'CI runner', style: 'heavy', lines: ['the suite: axx, with', 'stormbreaker’s packs', '', '· streams', '· stormbreaker-core', '· stormbreaker-rest', '· stormbreaker-kafka', '· stormbreaker-prometheus'] },
		{ id: 'orch', x: 60, y: 2, w: 36, h: 6, title: 'orchestrator', style: 'heavy', lines: ['one per environment', 'runs · the gate · the footprint', 'the journal'] },
		{ id: 'parcels', x: 37, y: 10, w: 30, h: 12, title: 'parcels team', style: 'zone' },
		{ id: 'p-disp', x: 48, y: 12, w: 17, h: 3, title: 'dispatcher', lines: ['its hooks'] },
		{ id: 'p-svc', x: 39, y: 17, w: 13, h: 4, title: 'parcels', lines: ['service', '2 regions'] },
		{ id: 'p-data', x: 54, y: 17, w: 11, h: 4, title: 'data', lines: ['parcels'] },
		{ id: 'tracking', x: 69, y: 10, w: 29, h: 12, title: 'tracking team', style: 'zone' },
		{ id: 't-disp', x: 80, y: 12, w: 17, h: 3, title: 'dispatcher', lines: ['its hooks'] },
		{ id: 't-svc', x: 71, y: 17, w: 13, h: 4, title: 'tracking', lines: ['service'] },
		{ id: 't-data', x: 86, y: 17, w: 11, h: 4, title: 'data', lines: ['Postgres'] },
		{ id: 'prom', x: 37, y: 24, w: 22, h: 4, title: 'Prometheus', lines: ['per app or region', 'or one global view'] },
		{ id: 'courier', x: 69, y: 24, w: 24, h: 4, title: 'courier mock', lines: ['axx-wiremock', 'in the cluster only'] },
	],
	edges: [
		{ id: 'suite-orch', path: [[30, 5], [59, 5]] },
		{ id: 'orch-p', path: [[66, 8], [66, 9], [55, 9], [55, 11]] },
		{ id: 'orch-t', path: [[88, 8], [88, 11]] },
		{ id: 'p-own', path: [[55, 15], [55, 16]] },
		{ id: 't-own', path: [[87, 15], [87, 16]] },
		{ id: 'load', path: [[30, 14], [36, 14], [36, 18], [38, 18]] },
		{ id: 'p-write', path: [[52, 18], [53, 18]] },
		{ id: 't-write', path: [[84, 18], [85, 18]] },
		{ id: 'events', path: [[45, 21], [45, 23], [77, 23], [77, 21]] },
		{ id: 'collect', path: [[80, 21], [80, 23]] },
		{ id: 'slo', path: [[12, 17], [12, 26], [36, 26]] },
	],
	texts: [
		{ x: 35, y: 4, text: 'open, declare, heartbeat' },
		{ x: 30, y: 13, text: 'load' },
		{ x: 57, y: 15, text: 'prepare' },
		{ x: 57, y: 16, text: 'clean up' },
		{ x: 89, y: 15, text: 'prepare' },
		{ x: 89, y: 16, text: 'clean up' },
		{ x: 58, y: 22, text: 'events' },
		{ x: 82, y: 22, text: 'books collections' },
		{ x: 14, y: 25, text: 'SLO queries' },
		{ x: 68, y: 9, text: 'notify ↓  report ↑' },
	],
	steps: [
		{
			name: 'Open',
			text: 'The suite opens a run on the environment’s orchestrator. Every dispatcher in the run’s group is notified, and takes part.',
			lit: [
				{ edge: 'suite-orch', kind: 'coord', packets: 2 },
				{ edge: 'orch-p', kind: 'notify', packets: 1 },
				{ edge: 'orch-t', kind: 'notify', packets: 1 },
			],
			texts: [{ x: 62, y: 6, text: 'gate: closed', kind: 'fail' }],
		},
		{
			name: 'Prepare',
			text: 'The suite declares every identity its streams will create. Each team’s dispatcher derives what its own systems will create from them, declares that, and prepares.',
			lit: [
				{ edge: 'suite-orch', kind: 'coord', packets: 3 },
				{ edge: 'orch-p', kind: 'notify', packets: 1 },
				{ edge: 'orch-p', kind: 'coord', packets: 2, reverse: true },
				{ edge: 'orch-t', kind: 'notify', packets: 1 },
				{ edge: 'orch-t', kind: 'coord', packets: 1, reverse: true },
				{ edge: 'p-own', kind: 'own', packets: 1, speed: 4 },
				{ edge: 't-own', kind: 'own', packets: 1, speed: 4 },
			],
			texts: [{ x: 62, y: 6, text: 'gate: closed', kind: 'fail' }],
		},
		{
			name: 'The gate opens',
			text: 'Every required dispatcher has prepared, in a round that began from the footprint as it is now. There is nothing left to derive, so traffic may flow.',
			marks: [{ box: 'orch', kind: 'ok' }],
			texts: [{ x: 62, y: 6, text: 'gate: open', kind: 'ok' }],
		},
		{
			name: 'Traffic',
			text: 'Load goes out at the declared rate, open model, through the real front door. The parcels service feeds the tracking service, which books collections with a mocked courier. The suite renews its lease throughout.',
			lit: [
				{ edge: 'load', kind: 'traffic', packets: 3, speed: 18 },
				{ edge: 'p-write', kind: 'traffic' },
				{ edge: 'events', kind: 'traffic', packets: 4, speed: 18 },
				{ edge: 't-write', kind: 'traffic' },
				{ edge: 'collect', kind: 'traffic', packets: 1, speed: 4 },
				{ edge: 'suite-orch', kind: 'coord', packets: 1, speed: 8 },
			],
			texts: [{ x: 62, y: 6, text: 'gate: open', kind: 'ok' }],
		},
		{
			name: 'Judge',
			text: 'The run’s window closes. The suite judges its SLOs over that window from Prometheus: buckets from every server merged, never percentiles averaged.',
			lit: [{ edge: 'slo', kind: 'coord', packets: 3, speed: 18 }],
			texts: [{ x: 62, y: 6, text: 'window closed', kind: 'label' }],
		},
		{
			name: 'Clean up',
			text: 'Each dispatcher removes what its own team’s systems created, checks it is gone, and reports it removed and verified. No component touches another team’s data.',
			lit: [
				{ edge: 'suite-orch', kind: 'coord', packets: 1 },
				{ edge: 'orch-p', kind: 'notify', packets: 1 },
				{ edge: 'orch-p', kind: 'own', packets: 2, reverse: true },
				{ edge: 'orch-t', kind: 'notify', packets: 1 },
				{ edge: 'orch-t', kind: 'own', packets: 1, reverse: true },
				{ edge: 'p-own', kind: 'own', packets: 1, speed: 4 },
				{ edge: 't-own', kind: 'own', packets: 1, speed: 4 },
			],
			texts: [{ x: 62, y: 6, text: 'cleanup', kind: 'own' }],
		},
		{
			name: 'Closed',
			text: 'Every dispatcher completed, on the footprint as it is now. The run closes, and its journal tells what happened, who prepared, and what was cleaned up.',
			marks: [{ box: 'orch', kind: 'ok' }],
			texts: [{ x: 62, y: 6, text: 'run closed ✓', kind: 'ok' }],
		},
	],
};

const state = (id: string, x: number, y: number, w: number): import('../../lib/ascii-diagram').Box => ({ id, x, y, w, h: 3, title: id, inside: true });

/** The run lifecycle: each state in turn, then the two side paths. */
export const runLifecycle: Scene = {
	width: 84,
	height: 22,
	boxes: [
		state('opened', 0, 1, 10), state('preparing', 13, 1, 13), state('ready', 29, 1, 9), state('traffic', 41, 1, 11),
		state('traffic-complete', 55, 1, 20), state('settled', 60, 9, 11), state('cleanup', 40, 9, 11), state('closed', 22, 9, 10),
		{ id: 'failed', x: 14, y: 16, w: 10, h: 3, title: 'failed', inside: true, kind: 'fail' },
		{ id: 'abandoned', x: 52, y: 16, w: 13, h: 3, title: 'abandoned', inside: true, kind: 'fail' },
	],
	edges: [
		{ id: 'opened', path: [[10, 2], [12, 2]] }, { id: 'preparing', path: [[26, 2], [28, 2]] },
		{ id: 'ready', path: [[38, 2], [40, 2]] }, { id: 'traffic', path: [[52, 2], [54, 2]] },
		{ id: 'complete', path: [[65, 4], [65, 8]] }, { id: 'settled', path: [[59, 10], [51, 10]] },
		{ id: 'cleanup', path: [[39, 10], [32, 10]] },
		{ id: 'fails', path: [[19, 4], [19, 15]] },
		{ id: 'failed', path: [[24, 17], [43, 17], [43, 12]] },
		{ id: 'abandoned', path: [[51, 17], [47, 17], [47, 12]] },
	],
	texts: [
		{ x: 0, y: 20, text: 'the gate cannot open' },
		{ x: 47, y: 20, text: 'the suite goes silent or gives up' },
	],
	steps: [
		{ name: 'opened', text: 'The suite opens a run for a group, naming the dispatchers it requires. Every live instance of every dispatcher in the group is put on the run, and notified (run.opened).', marks: [{ box: 'opened', kind: 'coord' }] },
		{ name: 'preparing', text: 'The suite declares what its streams will create, and each dispatcher declares what its systems will derive from that, in rounds, until a round adds nothing (run.footprint-updated).', marks: [{ box: 'preparing', kind: 'coord' }], lit: [{ edge: 'opened', kind: 'coord' }] },
		{ name: 'ready', text: 'The readiness gate opens: every required instance has prepared, in a round that began from the footprint’s version now.', marks: [{ box: 'ready', kind: 'ok' }], lit: [{ edge: 'preparing', kind: 'coord' }] },
		{ name: 'traffic', text: 'The suite sends the traffic at its declared rates, and renews its lease throughout.', marks: [{ box: 'traffic', kind: 'traffic' }], lit: [{ edge: 'ready', kind: 'coord' }] },
		{ name: 'traffic-complete', text: 'The suite says its traffic is done, and the run’s window closes (run.traffic-complete).', marks: [{ box: 'traffic-complete', kind: 'coord' }], lit: [{ edge: 'traffic', kind: 'coord' }] },
		{ name: 'settled', text: 'The suite says the run has settled, once its checks are done (run.settled). Draining downstream is the scenario’s to wait for, such as a consumer group catching up.', marks: [{ box: 'settled', kind: 'coord' }], lit: [{ edge: 'complete', kind: 'coord' }] },
		{ name: 'cleanup', text: 'Every dispatcher removes what its systems created and proves it, in rounds, until each is complete (run.cleanup-ready).', marks: [{ box: 'cleanup', kind: 'own' }], lit: [{ edge: 'settled', kind: 'coord' }] },
		{ name: 'closed', text: 'Every instance completed, or is unreachable, on the footprint as it is now. The orchestrator closes the run. A cleanup still going at the cleanup deadline (24 h) closes unfinished.', marks: [{ box: 'closed', kind: 'ok' }], lit: [{ edge: 'cleanup', kind: 'own' }] },
		{ name: 'failed', text: 'The gate cannot open: a required instance is blocked, unreachable or silent, or the readiness timeout passes. The run goes to cleanup all the same, so whatever was prepared is removed.', marks: [{ box: 'failed', kind: 'fail' }, { box: 'cleanup', kind: 'own' }], lit: [{ edge: 'fails', kind: 'fail' }, { edge: 'failed', kind: 'fail' }] },
		{ name: 'abandoned', text: 'The suite stops renewing its lease, because its runner died, or gives the run up, because its scenario failed. The run goes to cleanup. Silence is a signal, and the mess is still someone’s.', marks: [{ box: 'abandoned', kind: 'fail' }, { box: 'cleanup', kind: 'own' }], lit: [{ edge: 'abandoned', kind: 'fail' }] },
	],
};

/** The readiness gate, converging on a version, built up step by step. */
export const readinessGate: Scene = {
	width: 97,
	height: 16,
	edges: [{ id: 'grew', path: [[46, 10], [48, 10], [48, 2]], from: 2 }],
	texts: [
		{ x: 0, y: 1, text: 'footprint', kind: 'title' }, { x: 0, y: 4, text: 'suite', kind: 'title' },
		{ x: 0, y: 7, text: 'parcels', kind: 'title' }, { x: 0, y: 10, text: 'tracking', kind: 'title' },
		{ x: 0, y: 13, text: 'the gate', kind: 'title' },
		{ x: 13, y: 1, text: 'v1 · 1,000 claims', kind: 'coord' },
		{ x: 13, y: 4, text: '[ declares 1,000 parcels ]', kind: 'coord' },
		{ x: 13, y: 13, text: 'closed ─────────────────────────────────────────────────────────────────', kind: 'fail' },
		{ x: 20, y: 7, text: '[ from v1: nothing to add ]', kind: 'sub', from: 1 },
		{ x: 20, y: 10, text: '[ from v1: derives 1,000 ]', kind: 'sub', from: 1 },
		{ x: 47, y: 1, text: 'v2 · 2,000 claims', kind: 'coord', from: 2 },
		{ x: 50, y: 7, text: 'prepared @ v1', kind: 'ok', from: 2, dimFrom: 4 },
		{ x: 50, y: 10, text: 'prepared @ v1', kind: 'ok', from: 2, dimFrom: 4 },
		{ x: 13, y: 14, text: 'prepared on v1, but the footprint is at v2: those rounds never saw it', kind: 'fail', from: 2, dimFrom: 4 },
		{ x: 65, y: 7, text: '[ from v2: nothing new ]', kind: 'sub', from: 3 },
		{ x: 65, y: 10, text: '[ from v2: nothing new ]', kind: 'sub', from: 3 },
		{ x: 90, y: 7, text: '@ v2 ✓', kind: 'ok', from: 4 },
		{ x: 90, y: 10, text: '@ v2 ✓', kind: 'ok', from: 4 },
		{ x: 87, y: 13, text: 'open ✓', kind: 'ok', from: 5 },
	],
	steps: [
		{ name: 'The suite declares', text: 'The suite declares the 1,000 parcels its stream will create. The footprint is at version 1, and the gate is closed: nobody has prepared on it yet.' },
		{ name: 'A round each, from v1', text: 'Each dispatcher reads the run, then walks the footprint as it was at version 1. The parcels team has nothing to add. The tracking team derives a tracking record for every parcel.' },
		{ name: 'The footprint grows', text: 'The tracking team declares its 1,000 records, so the footprint grows to version 2. Both rounds report prepared on version 1, which no longer counts: they never saw what grew.', lit: [{ edge: 'grew', kind: 'coord', packets: 2, speed: 8 }] },
		{ name: 'A look at v2', text: 'The footprint grew. Each dispatcher asks whether anything new was reported where its hook walks. The tracking team’s own records are not where either hook reads.' },
		{ name: 'Prepared on v2', text: 'Nothing new where they read, so both settle on version 2 without running their hooks again. A hook that walks the whole footprint runs a second round instead. A deeper cascade of teams only adds rounds.' },
		{ name: 'The gate opens', text: 'Every required instance has prepared on the footprint as it is now. That is a fact, not a timer, and the traffic flows.' },
	],
};

/** How a transition reaches every dispatcher. */
export const delivery: Scene = {
	width: 96,
	height: 19,
	boxes: [
		{ id: 'orch', x: 0, y: 4, w: 24, h: 5, title: 'orchestrator', style: 'heavy', lines: ['a transition:', 'cleanup opens'] },
		{ id: 'record', x: 2, y: 12, w: 20, h: 4, title: 'its record', lines: ['SQLite, durable', ''] },
		{ id: 'east', x: 72, y: 0, w: 24, h: 4, title: 'parcels · east', inside: true },
		{ id: 'west', x: 72, y: 5, w: 24, h: 4, title: 'parcels · west', inside: true },
		{ id: 'tracking', x: 72, y: 11, w: 24, h: 4, title: 'tracking', inside: true },
	],
	edges: [
		{ id: 'write', path: [[12, 9], [12, 11]] },
		{ id: 'n-east', path: [[24, 5], [46, 5], [46, 1], [71, 1]] },
		{ id: 'n-west', path: [[24, 6], [71, 6]] },
		{ id: 'n-tracking', path: [[24, 7], [50, 7], [50, 12], [71, 12]], from: 4 },
		{ id: 'cut', path: [[24, 7], [50, 7], [50, 9]], arrow: false, until: 3 },
		{ id: 'feed', path: [[84, 15], [84, 17], [12, 17], [12, 16]] },
	],
	texts: [{ x: 30, y: 18, text: 'GET /events?since=40' }],
	steps: [
		{ name: 'Written before it is sent', text: 'Cleanup opens. The orchestrator writes the transition to its record first, as event 41. A notification never announces something that is not recorded.', lit: [{ edge: 'write', kind: 'coord', packets: 1, speed: 3 }], texts: [{ x: 4, y: 14, text: 'seq 41 written', kind: 'coord' }] },
		{ name: 'Signed and sent', text: 'It sends a notification, signed with Standard Webhooks headers, to every instance on the run. Each carries no instructions: it says “look at this run”.', lit: [{ edge: 'n-east', kind: 'notify', packets: 2 }, { edge: 'n-west', kind: 'notify', packets: 2 }, { edge: 'cut', kind: 'notify', packets: 1 }], texts: [{ x: 4, y: 14, text: 'seq 41 written', kind: 'coord' }] },
		{ name: 'One does not arrive', text: 'Two instances answer 2xx. The tracking instance’s notification is refused. It is retried with backoff, and the journal says so if it never arrives.', lit: [{ edge: 'cut', kind: 'fail' }], texts: [{ x: 4, y: 14, text: 'seq 41 written', kind: 'coord' }, { x: 74, y: 2, text: '2xx ✓', kind: 'ok' }, { x: 74, y: 7, text: '2xx ✓', kind: 'ok' }, { x: 49, y: 10, text: '✕ refused', kind: 'fail' }] },
		{ name: 'The event feed backstops it', text: 'Every dispatcher also reads the event feed on a cadence. The tracking instance finds event 41 there, and catches up. The feed’s cursor never skips an event.', lit: [{ edge: 'feed', kind: 'coord', packets: 3, speed: 18 }], texts: [{ x: 4, y: 14, text: 'seq 41 written', kind: 'coord' }, { x: 74, y: 2, text: '2xx ✓', kind: 'ok' }, { x: 74, y: 7, text: '2xx ✓', kind: 'ok' }, { x: 74, y: 13, text: 'caught up ✓', kind: 'ok' }] },
		{ name: 'Each reads the run and acts', text: 'Notifications are claim checks. Each dispatcher reads the run and acts on its state, so a duplicate, a late one, or one out of order changes nothing.', texts: [{ x: 74, y: 2, text: 'cleans up its own', kind: 'own' }, { x: 74, y: 7, text: 'cleans up its own', kind: 'own' }, { x: 74, y: 13, text: 'cleans up its own', kind: 'own' }] },
	],
};

/** A row of the footprint, in its columns. */
const ledger = (who: string, store: string, ids: string, resolution: string) => who.padEnd(10) + store.padEnd(13) + ids.padEnd(12) + resolution;

/** A dispatcher's two hooks, against the footprint. */
export const dispatcherHooks: Scene = {
	width: 100,
	height: 18,
	boxes: [
		{ id: 'fp', x: 0, y: 0, w: 55, h: 12, title: 'the run’s footprint', style: 'heavy' },
		{ id: 'zone', x: 58, y: 0, w: 42, h: 14, title: 'tracking team’s dispatcher', style: 'zone' },
		{ id: 'prepare', x: 60, y: 2, w: 38, h: 4, title: 'Prepare', lines: ['reads the footprint, and declares', 'what tracking will create'] },
		{ id: 'cleanup', x: 60, y: 8, w: 38, h: 4, title: 'Cleanup', lines: ['removes its own claims’ rows,', 'and proves they are gone'] },
		{ id: 'data', x: 68, y: 15, w: 23, h: 3, title: 'its data · Postgres', inside: true },
	],
	edges: [
		{ id: 'read', path: [[55, 3], [59, 3]] },
		{ id: 'declare', path: [[59, 4], [55, 4]] },
		{ id: 'walk', path: [[55, 9], [59, 9]] },
		{ id: 'removed', path: [[59, 10], [55, 10]] },
		{ id: 'delete', path: [[79, 12], [79, 14]] },
	],
	texts: [
		{ x: 2, y: 2, text: ledger('who', 'store', 'identities', 'resolution'), kind: 'label' },
		{ x: 2, y: 4, text: ledger('suite', 'parcels', 'PX-1…1000', 'expected'), kind: 'sub' },
		{ x: 2, y: 6, text: ledger('tracking', 'tracking-db', 'TRK-1…1000', 'expected'), kind: 'sub', from: 2 },
		{ x: 2, y: 8, text: ledger('tracking', 'tracking-db', 'TRK-1…1000', 'removed-verified'), kind: 'sub', from: 5 },
	],
	steps: [
		{ name: 'The suite declares', text: 'Before any traffic, the suite declares the 1,000 parcels its stream will create. Every claim in the footprint says who made it, where, and what it is.', marks: [{ box: 'fp', kind: 'coord' }] },
		{ name: 'Prepare walks it', text: 'The tracking team’s Prepare hook walks the footprint, a page at a time, and reads the parcels.', lit: [{ edge: 'read', kind: 'coord', packets: 2, speed: 6 }] },
		{ name: 'Prepare declares', text: 'It declares the tracking record its service will write for each parcel. A declaration is a pure function of the footprint, so rounds converge.', lit: [{ edge: 'declare', kind: 'coord', packets: 2, speed: 6 }] },
		{ name: 'The traffic', text: 'The gate opens, and the traffic creates the parcels, and from them the 1,000 tracking records the team declared.', texts: [{ x: 2, y: 13, text: 'the traffic creates those 1,000 records', kind: 'traffic' }] },
		{ name: 'Cleanup walks its own', text: 'After the traffic, the Cleanup hook walks the claims its own team made, and nothing else.', lit: [{ edge: 'walk', kind: 'own', packets: 2, speed: 6 }] },
		{ name: 'Removed and proven', text: 'It deletes those rows from its own Postgres, checks they are gone, and reports them removed and verified. The footprint is appended to, never rewritten.', lit: [{ edge: 'delete', kind: 'own', packets: 1, speed: 3 }, { edge: 'removed', kind: 'own', packets: 2, speed: 6 }] },
	],
};

/** Where a service's metrics are: per app per region, per region, or one global view. */
const region = (name: string, ox: number): { boxes: import('../../lib/ascii-diagram').Box[]; edges: import('../../lib/ascii-diagram').Edge[] } => ({
	boxes: [
		{ id: `${name}`, x: ox, y: 1, w: 35, h: 15, title: name, style: 'zone' },
		{ id: `${name}-parcels`, x: ox + 22, y: 3, w: 12, h: 3, title: 'parcels', inside: true },
		{ id: `${name}-tracking`, x: ox + 22, y: 10, w: 12, h: 3, title: 'tracking', inside: true },
		{ id: `${name}-own-p`, x: ox + 2, y: 3, w: 18, h: 4, title: 'prometheus', lines: ['parcels’ own:', 'prom-0 | prom-1'], until: 0 },
		{ id: `${name}-own-t`, x: ox + 2, y: 10, w: 18, h: 4, title: 'prometheus', lines: ['tracking’s own'], until: 0 },
		{ id: `${name}-shared`, x: ox + 2, y: 3, w: 18, h: 11, title: 'prometheus', lines: ['shared by every', `app in ${name}`], from: 1, until: 1 },
		{ id: `${name}-agent`, x: ox + 2, y: 3, w: 18, h: 11, title: 'prometheus', lines: [`${name}’s, or an`, 'agent writing', 'to the view'], from: 2 },
	],
	edges: [
		{ id: `${name}-scrape-p`, path: [[ox + 21, 4], [ox + 20, 4]] },
		{ id: `${name}-scrape-t`, path: [[ox + 21, 11], [ox + 20, 11]] },
		{ id: `${name}-feed`, path: [[ox + 12, 14], [ox + 12, 17]], from: 2 },
	],
});
const eu1 = region('eu-1', 28), eu2 = region('eu-2', 65);

export const prometheusTopologies: Scene = {
	width: 100,
	height: 23,
	boxes: [
		{ id: 'suite', x: 0, y: 8, w: 22, h: 5, title: 'suite', style: 'heavy', lines: ['parcels-metrics', 'p99 · increase'] },
		...eu1.boxes, ...eu2.boxes,
		{ id: 'global', x: 34, y: 18, w: 50, h: 4, title: 'global view', style: 'heavy', lines: ['Thanos, Mimir, or a central Prometheus'], from: 2 },
	],
	edges: [
		...eu1.edges, ...eu2.edges,
		{ id: 'ask-eu-1', path: [[22, 9], [26, 9], [26, 5], [29, 5]], until: 1 },
		{ id: 'ask-eu-2', path: [[11, 7], [11, 0], [75, 0], [75, 2]], until: 1 },
		{ id: 'ask-global', path: [[11, 13], [11, 20], [33, 20]], from: 2 },
	],
	steps: [
		{
			name: 'Per app, per region',
			text: 'Each app runs a Prometheus of its own in each region or zone, here an HA pair. The service lists the parcels team’s Prometheus in each region, its replicas separated by |, so a pair counts once. It needs no selector: the server holds only that app.',
			lit: [{ edge: 'ask-eu-1', kind: 'coord', packets: 2 }, { edge: 'ask-eu-2', kind: 'coord', packets: 3, speed: 20 }],
			marks: [{ box: 'eu-1-own-p', kind: 'coord' }, { box: 'eu-2-own-p', kind: 'coord' }]
		},
		{
			name: 'Per region',
			text: 'One Prometheus in each region scrapes every app there. The service lists each region’s Prometheus, and a selector, app="parcels", picks the parcels team’s series out of everyone else’s.',
			lit: [{ edge: 'ask-eu-1', kind: 'coord', packets: 2 }, { edge: 'ask-eu-2', kind: 'coord', packets: 3, speed: 20 }],
			marks: [{ box: 'eu-1-shared', kind: 'coord' }, { box: 'eu-2-shared', kind: 'coord' }]
		},
		{
			name: 'Global',
			text: 'One view holds every region: Thanos, Mimir, or a central Prometheus. The service lists that one endpoint, with a selector; a region matcher in it judges some regions only. Checks merge the same way whichever it is.',
			lit: [{ edge: 'ask-global', kind: 'coord', packets: 2 }],
			marks: [{ box: 'global', kind: 'coord' }]
		},
	],
};
