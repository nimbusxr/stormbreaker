/**
 * The organization pages' diagrams (src/lib/ascii-diagram.ts): each scene, and what it shows in
 * words, for its SVG-less stage's label, the page's Markdown twin and text-only readers.
 */
import type { Box, Edge, Scene, Text } from '../../lib/ascii-diagram';

export interface OrgDiagram {
	title: string;
	text: string;
	scene: Scene;
}

/** Stormbreaker around an environment's applications, which do not change. */
export const aroundTheApps: OrgDiagram = {
	title: 'Stormbreaker around your applications',
	text: 'Three teams’ applications in a shared environment: parcels, tracking and billing, and a courier’s API outside it. Stormbreaker adds pieces around them and changes none: a suite on the CI runner, an orchestrator, and a dispatcher beside each application that takes part, working through the team’s own door. A mock stands in for the courier, and another for billing, a team that is not taking part, so none of the run’s traffic reaches them. Whatever a mock has no stub for falls through to the real one. The traffic goes through the applications’ ordinary front doors, and each dispatcher cleans up through its own team’s door.',
	scene: {
		width: 100,
		height: 25,
		boxes: [
			{ id: 'env', x: 19, y: 0, w: 81, h: 25, title: 'the shared environment', style: 'zone' },
			{ id: 'runner', x: 0, y: 12, w: 17, h: 4, title: 'CI runner', style: 'heavy', kind: 'coord', lines: ['the suite'], from: 1 },
			{ id: 'orch', x: 30, y: 1, w: 36, h: 4, title: 'orchestrator', style: 'heavy', kind: 'coord', lines: ['tracks the runs; holds no data'], from: 1 },
			{ id: 'p-disp', x: 22, y: 7, w: 15, h: 3, title: 'dispatcher', kind: 'coord', lines: ['east · west'], from: 1 },
			{ id: 't-disp', x: 48, y: 7, w: 15, h: 3, title: 'dispatcher', kind: 'coord', lines: ['shared'], from: 1 },
			{ id: 'parcels', x: 22, y: 12, w: 21, h: 4, title: 'parcels', lines: ['east · west', 'unchanged'] },
			{ id: 'tracking', x: 48, y: 12, w: 21, h: 4, title: 'tracking', lines: ['service · Postgres', 'unchanged'] },
			{ id: 'courier', x: 74, y: 12, w: 24, h: 4, title: 'courier', style: 'zone', lines: ['a partner’s API,', 'not in this cluster'], until: 1 },
			{ id: 'courier-mock', x: 74, y: 12, w: 24, h: 4, title: 'courier mock', kind: 'coord', lines: ['a stand-in: the rest', 'passes on to courier'], from: 2 },
			{ id: 'billing', x: 48, y: 20, w: 21, h: 4, title: 'billing', lines: ['another team,', 'unchanged'], until: 1 },
			{ id: 'billing-mock', x: 48, y: 20, w: 21, h: 4, title: 'billing mock', kind: 'coord', lines: ['a stand-in for a', 'team sitting out'], from: 2 },
			{ id: 'billing-out', x: 74, y: 20, w: 24, h: 4, title: 'billing', style: 'zone', lines: ['not taking part:', 'sees none of the run'], from: 2 },
		],
		edges: [
			{ id: 'declare', path: [[14, 11], [14, 2], [29, 2]], from: 1 },
			{ id: 'load', path: [[17, 13], [21, 13]], from: 1 },
			{ id: 'notify-p', path: [[33, 5], [33, 6]], from: 1 },
			{ id: 'notify-t', path: [[56, 5], [56, 6]], from: 1 },
			{ id: 'door-p', path: [[29, 10], [29, 11]], from: 1 },
			{ id: 'door-t', path: [[55, 10], [55, 11]], from: 1 },
			{ id: 'kafka', path: [[43, 13], [47, 13]] },
			{ id: 'courier', path: [[69, 13], [73, 13]] },
			{ id: 'billing', path: [[40, 16], [40, 21], [47, 21]] },
			{ id: 'billing-pass', path: [[69, 21], [73, 21]], from: 2 },
		],
		texts: [
			{ x: 43, y: 14, text: 'kafka' },
			{ x: 31, y: 10, text: 'its own door', kind: 'own', from: 1 },
			{ x: 57, y: 10, text: 'its own door', kind: 'own', from: 1 },
		],
		steps: [
			{ name: 'The applications, as they are', text: 'Three teams’ applications in a shared environment, and a courier’s API outside it. Stormbreaker asks none of them to change: no SDK, no test flag, no tenant column, no new infrastructure.' },
			{ name: 'What stormbreaker adds, around them', text: 'A suite on the CI runner, one small orchestrator that only keeps track of runs, and a dispatcher beside each application that takes part. A dispatcher works through the door its team already has: an admin API, a database, its own tools.', lit: [{ edge: 'declare', kind: 'coord', packets: 2 }] },
			{ name: 'Mocks stand in', text: 'A mock stands in for the courier, a partner nobody here owns. Another stands in for billing, a team that is not taking part: the URL parcels already calls now leads to the mock, and billing sees none of the run. Whatever a mock has no stub for falls through to the real one, so traffic outside the run goes where it always went.' },
			{ name: 'The traffic goes through the front doors', text: 'The suite sends ordinary requests through the front door everyone uses. Parcels feeds tracking through Kafka as it always does. The calls that would leave the run land on the mocks. Nothing inside the applications changes.', lit: [
				{ edge: 'load', kind: 'traffic', packets: 2, speed: 8 },
				{ edge: 'kafka', kind: 'traffic', packets: 2, speed: 8 },
				{ edge: 'courier', kind: 'traffic', packets: 2, speed: 8 },
				{ edge: 'billing', kind: 'traffic', packets: 2, speed: 10 },
			] },
			{ name: 'Each team cleans through its own door', text: 'After the traffic, each dispatcher removes what its own application created, through its own team’s door, and proves it. Taking part is optional: a team joins by deploying a dispatcher, and leaves by deleting it.', lit: [
				{ edge: 'notify-p', kind: 'notify', packets: 1, speed: 3 },
				{ edge: 'notify-t', kind: 'notify', packets: 1, speed: 3 },
				{ edge: 'door-p', kind: 'own', packets: 1, speed: 3 },
				{ edge: 'door-t', kind: 'own', packets: 1, speed: 3 },
			] },
		],
	},
};

/** An environment panel: its orchestrator, and its teams with their dispatchers. */
const panel = (name: string, ox: number): { boxes: Box[]; edges: Edge[] } => ({
	boxes: [
		{ id: `${name}`, x: ox, y: 4, w: 32, h: 20, title: name, style: 'zone' },
		{ id: `${name}-ci`, x: ox + 6, y: 0, w: 20, h: 3, title: `CI · ${name}`, inside: true, style: 'heavy' },
		{ id: `${name}-ingress`, x: ox + 8, y: 5, w: 17, h: 3, title: 'ingress · TLS', inside: true },
		{ id: `${name}-orch`, x: ox + 2, y: 10, w: 28, h: 3, title: 'orchestrator', style: 'heavy', lines: ['its own volume and token'] },
		{ id: `${name}-parcels`, x: ox + 2, y: 15, w: 14, h: 4, title: 'parcels', lines: ['east, west', 'dispatchers'] },
		{ id: `${name}-tracking`, x: ox + 17, y: 15, w: 13, h: 4, title: 'tracking', lines: ['Postgres', 'dispatcher'] },
		{ id: `${name}-courier`, x: ox + 2, y: 20, w: 28, h: 3, title: 'courier mock', inside: true },
	],
	edges: [
		{ id: `${name}-in`, path: [[ox + 16, 3], [ox + 16, 4]] },
		{ id: `${name}-to-orch`, path: [[ox + 16, 8], [ox + 16, 9]] },
		{ id: `${name}-notify-p`, path: [[ox + 8, 13], [ox + 8, 14]] },
		{ id: `${name}-notify-t`, path: [[ox + 23, 13], [ox + 23, 14]] },
	],
});
const dev = panel('dev', 1), staging = panel('staging', 34), perf = panel('perf', 67);

/** One orchestrator per environment, and nothing shared between them. */
export const orgEnvironments: OrgDiagram = {
	title: 'One orchestrator per environment',
	text: 'Three environments, dev, staging and perf, side by side. Each has its own orchestrator with its own volume and token, its own teams with their dispatchers, and its own courier mock. Each CI pipeline points at one environment, through that environment’s ingress. A run opened in staging reaches staging’s orchestrator, and only staging’s dispatchers hear of it: nothing crosses between environments.',
	scene: {
		width: 100,
		height: 24,
		boxes: [...dev.boxes, ...staging.boxes, ...perf.boxes],
		edges: [...dev.edges, ...staging.edges, ...perf.edges],
		steps: [
			{ name: 'Environments share nothing', text: 'Each shared environment has its own orchestrator, with its own volume and token, and its own dispatchers. A CI pipeline points at one of them, with STORMBREAKER_ORCHESTRATOR_URL.' },
			{ name: 'A pipeline runs against staging', text: 'The suite opens a run on staging’s orchestrator, through staging’s ingress, where TLS ends. Dev and perf know nothing of it.', lit: [
				{ edge: 'staging-in', kind: 'coord', packets: 1, speed: 3 },
				{ edge: 'staging-to-orch', kind: 'coord', packets: 1, speed: 3 },
			], marks: [{ box: 'staging-ci', kind: 'coord' }] },
			{ name: 'Only staging’s dispatchers hear of it', text: 'Staging’s orchestrator notifies staging’s dispatchers, and no one else’s. The same teams run dispatchers in every environment, each registered with that environment’s orchestrator alone.', lit: [
				{ edge: 'staging-notify-p', kind: 'notify', packets: 1, speed: 3 },
				{ edge: 'staging-notify-t', kind: 'notify', packets: 1, speed: 3 },
			], marks: [{ box: 'staging-orch', kind: 'coord' }] },
		],
	},
};

/** The paths in one environment: what crosses its edge, and what stays inside. */
export const orgPaths: OrgDiagram = {
	title: 'The paths in one environment',
	text: 'One environment. The CI runner, outside the cluster, reaches the orchestrator and the parcels service through the ingress, where TLS ends, and queries Prometheus. Inside the cluster, the orchestrator sends signed notifications to each team’s dispatcher, across namespaces, and the dispatchers report back. The traffic goes from the parcels service through Kafka to the tracking service, which books collections with the courier mock. Each dispatcher prepares and cleans up through its own team’s door: the parcels service’s admin API, the tracking team’s Postgres.',
	scene: {
		width: 100,
		height: 29,
		boxes: [
			{ id: 'cluster', x: 22, y: 0, w: 78, h: 29, title: 'the cluster', style: 'zone' },
			{ id: 'runner', x: 0, y: 11, w: 15, h: 4, title: 'CI runner', style: 'heavy', lines: ['the suite'] },
			{ id: 'ingress', x: 18, y: 11, w: 12, h: 4, title: 'ingress', inside: true, style: 'heavy', lines: ['TLS ends'] },
			{ id: 'orch', x: 40, y: 1, w: 31, h: 4, title: 'orchestrator', style: 'heavy', lines: ['ns stormbreaker · its volume'] },
			{ id: 'parcels', x: 32, y: 8, w: 35, h: 10, title: 'parcels', style: 'zone' },
			{ id: 'p-svc', x: 34, y: 10, w: 15, h: 4, title: 'service', lines: ['east · west'] },
			{ id: 'p-disp', x: 50, y: 10, w: 15, h: 4, title: 'dispatcher', lines: ['east · west'] },
			{ id: 'tracking', x: 68, y: 8, w: 30, h: 10, title: 'tracking', style: 'zone' },
			{ id: 't-svc', x: 70, y: 10, w: 13, h: 4, title: 'service', lines: ['consumer'] },
			{ id: 't-db', x: 85, y: 10, w: 12, h: 3, title: 'Postgres', inside: true },
			{ id: 't-disp', x: 83, y: 14, w: 14, h: 3, title: 'dispatcher', inside: true },
			{ id: 'kafka', x: 32, y: 21, w: 14, h: 3, title: 'Kafka', inside: true },
			{ id: 'prom', x: 50, y: 21, w: 21, h: 3, title: 'Prometheus · e, w', inside: true },
			{ id: 'courier', x: 74, y: 21, w: 16, h: 3, title: 'courier mock', inside: true },
		],
		edges: [
			{ id: 'in', path: [[15, 12], [17, 12]] },
			{ id: 'to-orch', path: [[24, 10], [24, 2], [39, 2]] },
			{ id: 'front', path: [[30, 12], [33, 12]] },
			{ id: 'slo', path: [[7, 15], [7, 26], [60, 26], [60, 24]] },
			{ id: 'notify-p', path: [[56, 5], [56, 9]] },
			{ id: 'notify-t', path: [[66, 5], [66, 7], [84, 7], [84, 13]] },
			{ id: 'door-p', path: [[49, 12]], dir: 'left' },
			{ id: 'door-t', path: [[90, 13]], dir: 'up' },
			{ id: 'events', path: [[39, 14], [39, 20]] },
			{ id: 'consume', path: [[46, 22], [48, 22], [48, 19], [76, 19], [76, 14]] },
			{ id: 'collect', path: [[80, 14], [80, 20]] },
		],
		texts: [
			{ x: 26, y: 3, text: 'open · declare' },
			{ x: 26, y: 5, text: 'heartbeat · publish' },
			{ x: 68, y: 6, text: 'notify ↓  report ↑' },
			{ x: 41, y: 15, text: 'own door', kind: 'own' },
			{ x: 41, y: 19, text: 'events' },
			{ x: 9, y: 25, text: 'SLO queries' },
		],
		steps: [
			{ name: 'Through the edge', text: 'The suite reaches the orchestrator from the CI runner through the ingress, where TLS ends, with its team’s token. It opens the run, declares, renews its lease every 10 s, and publishes each phase. Stormbreaker never tunnels.', lit: [
				{ edge: 'in', kind: 'coord', packets: 1, speed: 3 }, { edge: 'to-orch', kind: 'coord', packets: 2, speed: 10 },
			] },
			{ name: 'Notifications, inside the cluster', text: 'The orchestrator sends signed notifications to the callback URL each dispatcher registered, across namespaces. A NetworkPolicy must let them through; the event feed backstops any that do not arrive.', lit: [
				{ edge: 'notify-p', kind: 'notify', packets: 1, speed: 5 }, { edge: 'notify-t', kind: 'notify', packets: 2, speed: 12 },
			] },
			{ name: 'Reports, from each team', text: 'Each dispatcher reaches the orchestrator from inside the cluster: it registers every 10 s, which renews its lease, reports its status and footprint, and reads the event feed.', lit: [
				{ edge: 'notify-p', kind: 'coord', packets: 1, speed: 5, reverse: true }, { edge: 'notify-t', kind: 'coord', packets: 2, speed: 12, reverse: true },
			] },
			{ name: 'Traffic, through the front door', text: 'The load goes from the runner through the parcels service’s own ingress, on to Kafka, to the tracking service, and to the courier mock, as production traffic would.', lit: [
				{ edge: 'in', kind: 'traffic', packets: 1, speed: 3 }, { edge: 'front', kind: 'traffic', packets: 1, speed: 3 },
				{ edge: 'events', kind: 'traffic', packets: 2, speed: 6 }, { edge: 'consume', kind: 'traffic', packets: 3, speed: 14 },
				{ edge: 'collect', kind: 'traffic', packets: 2, speed: 6 },
			] },
			{ name: 'SLO checks', text: 'The suite queries each region’s Prometheus from the runner, through its ingress, over the run’s window.', lit: [{ edge: 'slo', kind: 'coord', packets: 3, speed: 18 }] },
			{ name: 'Cleanup, each through its own door', text: 'Each dispatcher prepares, removes and verifies inside its own team’s namespace: the parcels dispatchers through the service’s admin API, the tracking dispatcher in its Postgres.', lit: [
				{ edge: 'door-p', kind: 'own' }, { edge: 'door-t', kind: 'own' },
			], marks: [{ box: 'p-svc', kind: 'own' }, { box: 't-db', kind: 'own' }] },
		],
	},
};

const G = 68, C = 84; // the gate's column and close's
const row = { east: 3, west: 6, tracking: 9, billing: 12 };
const at = (x: number, y: number, text: string, kind: Text['kind'] = 'label'): Text => ({ x, y, text, kind });

/** A group's members: the required ones hold the gate; every one holds close. */
export const groupsAndRequired: OrgDiagram = {
	title: 'A group, its required dispatchers, and close',
	text: 'A scenario tagged @stormbreaker-group/delivery and @stormbreaker-requires/parcels,tracking opens a run for the delivery group. Its four members are on the run: parcels/east, parcels/west and tracking/shared, which are required, and billing/shared, which is not. The readiness gate opens once the three required instances have prepared, while billing is still preparing. The run closes only when all four, billing included, have completed their cleanup.',
	scene: {
		width: 96,
		height: 17,
		boxes: [
			{ id: 'scenario', x: 0, y: 2, w: 28, h: 6, title: 'scenario', style: 'heavy', lines: ['@stormbreaker-group/', '  delivery', '@stormbreaker-requires/', '  parcels,tracking'] },
			{ id: 'group', x: 32, y: 0, w: 34, h: 17, title: 'group delivery', style: 'zone' },
			{ id: 'east', x: 34, y: 2, w: 30, h: 3, title: 'parcels/east     required', inside: true },
			{ id: 'west', x: 34, y: 5, w: 30, h: 3, title: 'parcels/west     required', inside: true },
			{ id: 'tracking', x: 34, y: 8, w: 30, h: 3, title: 'tracking/shared  required', inside: true },
			{ id: 'billing', x: 34, y: 11, w: 30, h: 3, title: 'billing/shared   optional', inside: true },
		],
		edges: [{ id: 'opens', path: [[28, 4], [31, 4]] }],
		texts: [at(G, 1, 'the gate', 'title'), at(C, 1, 'close', 'title'), at(G, 14, '────────────', 'line'), at(C, 14, '────────────', 'line')],
		steps: [
			{ name: 'A run opens', text: 'The scenario opens a run for the delivery group. Every live instance of every member is put on the run and notified, required or not.', lit: [{ edge: 'opens', kind: 'coord', packets: 1, speed: 2 }], texts: [
				at(G, row.east, 'preparing…'), at(G, row.west, 'preparing…'), at(G, row.tracking, 'preparing…'), at(G, row.billing, '— not held'),
				at(G, 15, 'closed', 'fail'),
			] },
			{ name: 'Required ones hold the gate', text: 'The gate waits for the required instances only. The two parcels instances have prepared; tracking is still working.', marks: [{ box: 'east', kind: 'ok' }, { box: 'west', kind: 'ok' }], texts: [
				at(G, row.east, 'prepared ✓', 'ok'), at(G, row.west, 'prepared ✓', 'ok'), at(G, row.tracking, 'preparing…'), at(G, row.billing, '— not held'),
				at(G, 15, 'closed', 'fail'),
			] },
			{ name: 'The gate opens', text: 'Tracking has prepared, so every required instance has, and the traffic flows. Billing is optional: it is still preparing, and the gate did not wait for it.', marks: [{ box: 'east', kind: 'ok' }, { box: 'west', kind: 'ok' }, { box: 'tracking', kind: 'ok' }], texts: [
				at(G, row.east, 'prepared ✓', 'ok'), at(G, row.west, 'prepared ✓', 'ok'), at(G, row.tracking, 'prepared ✓', 'ok'), at(G, row.billing, '— not held'),
				at(G, 15, 'open ✓', 'ok'),
			] },
			{ name: 'Every member cleans up', text: 'Cleanup is for every instance on the run, required or not. Three have completed; billing is still cleaning up, and the run stays open for it.', marks: [{ box: 'east', kind: 'own' }, { box: 'west', kind: 'own' }, { box: 'tracking', kind: 'own' }], texts: [
				at(G, row.east, 'prepared ✓', 'ok'), at(G, row.west, 'prepared ✓', 'ok'), at(G, row.tracking, 'prepared ✓', 'ok'), at(G, row.billing, '— not held'),
				at(C, row.east, 'completed ✓', 'own'), at(C, row.west, 'completed ✓', 'own'), at(C, row.tracking, 'completed ✓', 'own'), at(C, row.billing, 'cleaning…'),
				at(G, 15, 'open ✓', 'ok'), at(C, 15, 'waits', 'label'),
			] },
			{ name: 'The run closes', text: 'Billing has completed too. Only now, with every instance on the run completed, does the run close.', marks: [{ box: 'east', kind: 'own' }, { box: 'west', kind: 'own' }, { box: 'tracking', kind: 'own' }, { box: 'billing', kind: 'own' }], texts: [
				at(G, row.east, 'prepared ✓', 'ok'), at(G, row.west, 'prepared ✓', 'ok'), at(G, row.tracking, 'prepared ✓', 'ok'), at(G, row.billing, '— not held'),
				at(C, row.east, 'completed ✓', 'own'), at(C, row.west, 'completed ✓', 'own'), at(C, row.tracking, 'completed ✓', 'own'), at(C, row.billing, 'completed ✓', 'own'),
				at(G, 15, 'open ✓', 'ok'), at(C, 15, 'closed ✓', 'ok'),
			] },
		],
	},
};

/** An environment panel in a later stage of a rollout: its orchestrator, and its groups' teams. */
const stagePanel = (name: string, ox: number, w: number, step: number, rows: [string, string, boolean][], groupLabel: string): { boxes: Box[]; texts: Text[] } => ({
	boxes: [
		{ id: `${name}-${step}`, x: ox, y: 1, w, h: 10 + rows.length * 2, title: name, style: 'zone', from: step, until: step },
		{ id: `${name}-${step}-orch`, x: ox + 2, y: 3, w: w - 4, h: 3, title: 'orchestrator', style: 'heavy', lines: ['one per environment'], from: step, until: step },
	],
	texts: [
		{ x: ox + 3, y: 7, text: groupLabel, kind: 'label', from: step, until: step },
		...rows.flatMap(([team, where, dispatcher], i): Text[] => [
			{ x: ox + 3, y: 9 + i * 2, text: team, kind: 'title', from: step, until: step },
			{ x: ox + 14, y: 9 + i * 2, text: where, kind: 'sub', from: step, until: step },
			...(dispatcher ? [{ x: ox + w - 13, y: 9 + i * 2, text: 'dispatcher', kind: 'coord' as const, from: step, until: step }] : []),
		]),
	],
});
const flowRows: [string, string, boolean][] = [['parcels', 'east · west', true], ['tracking', 'shared', true], ['billing', 'shared', true], ['courier', 'a mock', false]];
const staging3 = stagePanel('staging', 18, 40, 2, flowRows, 'group delivery');
const perf3 = stagePanel('perf', 60, 40, 2, flowRows, 'group delivery');
const orgRows: [string, string, boolean][] = [['delivery', '', false], ['returns', '', false], ['billing', '', false]];
const dev4 = stagePanel('dev', 17, 27, 3, orgRows, 'groups');
const staging4 = stagePanel('staging', 45, 27, 3, orgRows, 'groups');
const perf4 = stagePanel('perf', 73, 27, 3, orgRows, 'groups');

/** Adopting stormbreaker, from one team alone to the organization. */
export const rollout: OrgDiagram = {
	title: 'Rolling stormbreaker out',
	text: 'Four stages. One team alone: its own orchestrator in its namespace, its service in two regions with a dispatcher, and a stand-in for its tracking neighbor, a mock or a topic nobody else reads. Two teams: the orchestrator moves to a platform namespace, the tracking team takes part with a dispatcher of its own in place of the stand-in, and a courier mock stands in for the partner. A flow: every team on the delivery path has a dispatcher, in a second environment too. The organization: several groups, in every shared environment, each environment with one orchestrator.',
	scene: {
		width: 100,
		height: 20,
		boxes: [
			{ id: 'runner', x: 0, y: 8, w: 15, h: 3, title: 'CI runner', style: 'heavy', lines: ['your suite'], until: 1 },
			{ id: 'env', x: 18, y: 0, w: 82, h: 20, title: 'staging', style: 'zone', until: 1 },
			// 1: one team, alone.
			{ id: 'ns1', x: 21, y: 2, w: 50, h: 16, title: 'parcels namespace', style: 'zone', until: 0 },
			{ id: 'orch1', x: 23, y: 4, w: 19, h: 3, title: 'orchestrator', style: 'heavy', lines: ['yours, for now'], until: 0 },
			{ id: 'svc1', x: 48, y: 4, w: 20, h: 3, title: 'parcels', lines: ['east · west'], until: 0 },
			{ id: 'disp1', x: 23, y: 11, w: 19, h: 3, title: 'dispatcher', kind: 'coord', lines: ['east · west'], until: 0 },
			{ id: 'standin', x: 74, y: 4, w: 24, h: 5, title: 'tracking stand-in', kind: 'coord', lines: ['a mock, or a topic', 'nobody else reads'], until: 0 },
			// 2: two teams.
			{ id: 'platform', x: 21, y: 2, w: 23, h: 7, title: 'platform', style: 'zone', from: 1, until: 1 },
			{ id: 'orch2', x: 23, y: 4, w: 19, h: 3, title: 'orchestrator', style: 'heavy', lines: ['one, shared'], from: 1, until: 1 },
			{ id: 'courier2', x: 21, y: 12, w: 23, h: 3, title: 'courier mock', kind: 'coord', lines: ['a partner’s stand-in'], from: 1, until: 1 },
			{ id: 'parcels2', x: 46, y: 2, w: 26, h: 17, title: 'parcels', style: 'zone', from: 1, until: 1 },
			{ id: 'svc2', x: 48, y: 4, w: 22, h: 3, title: 'parcels', lines: ['east · west'], from: 1, until: 1 },
			{ id: 'disp2', x: 48, y: 12, w: 22, h: 3, title: 'dispatcher', kind: 'coord', lines: ['east · west'], from: 1, until: 1 },
			{ id: 'tracking2', x: 74, y: 2, w: 25, h: 17, title: 'tracking', style: 'zone', from: 1, until: 1 },
			{ id: 'tsvc2', x: 76, y: 4, w: 21, h: 3, title: 'tracking', lines: ['service · Postgres'], from: 1, until: 1 },
			{ id: 'tdisp2', x: 76, y: 12, w: 21, h: 3, title: 'dispatcher', kind: 'coord', lines: ['shared'], from: 1, until: 1 },
			// 3 and 4: a flow, then the organization.
			{ id: 'runner3', x: 0, y: 8, w: 15, h: 3, title: 'CI runner', style: 'heavy', lines: ['2 pipelines'], from: 2, until: 2 },
			...staging3.boxes, ...perf3.boxes, ...dev4.boxes, ...staging4.boxes, ...perf4.boxes,
		],
		edges: [
			{ id: 'to-orch', path: [[15, 9], [19, 9], [19, 5], [22, 5]], until: 1 },
			{ id: 'front', path: [[13, 7], [13, 1], [58, 1], [58, 3]], until: 1 },
			{ id: 'notify1', path: [[32, 7], [32, 10]], until: 0 },
			{ id: 'door1', path: [[42, 12], [57, 12], [57, 7]], until: 0 },
			{ id: 'out1', path: [[68, 5], [73, 5]], until: 0 },
			{ id: 'events2', path: [[70, 5], [75, 5]], from: 1, until: 1 },
			{ id: 'door-p2', path: [[58, 11], [58, 7]], from: 1, until: 1 },
			{ id: 'door-t2', path: [[86, 11], [86, 7]], from: 1, until: 1 },
			{ id: 'to-staging', path: [[15, 9], [17, 9], [17, 4], [19, 4]], from: 2, until: 2 },
			{ id: 'to-perf', path: [[13, 7], [13, 0], [78, 0], [78, 2]], from: 2, until: 2 },
		],
		texts: [
			{ x: 44, y: 11, text: 'own door', kind: 'own', until: 0 },
			{ x: 60, y: 9, text: 'own door', kind: 'own', from: 1, until: 1 },
			{ x: 88, y: 9, text: 'own door', kind: 'own', from: 1, until: 1 },
			...staging3.texts, ...perf3.texts, ...dev4.texts, ...staging4.texts, ...perf4.texts,
			{ x: 0, y: 9, text: 'a pipeline', kind: 'label', from: 3 }, { x: 0, y: 10, text: 'points at one', kind: 'label', from: 3 }, { x: 0, y: 11, text: 'environment', kind: 'label', from: 3 },
		],
		steps: [
			{ name: 'One team, alone', text: 'You can load test your own application in a QA or staging environment without anyone else. Run the orchestrator in your own namespace, deploy a dispatcher for your service, and keep your traffic from your neighbors: a mock of the tracking service, or a topic nobody else reads.', lit: [{ edge: 'out1', kind: 'traffic', packets: 1, speed: 3 }, { edge: 'door1', kind: 'own', packets: 1, speed: 8 }] },
			{ name: 'Two teams', text: 'The orchestrator moves to a platform namespace, run by whoever owns the environment. The tracking team joins by deploying a dispatcher of its own, and its real service replaces the stand-in. A mock still stands in for the courier, whom nobody here owns.', lit: [{ edge: 'events2', kind: 'traffic', packets: 1, speed: 3 }, { edge: 'door-p2', kind: 'own', packets: 1, speed: 3 }, { edge: 'door-t2', kind: 'own', packets: 1, speed: 3 }] },
			{ name: 'A flow, and a second environment', text: 'Every team on the delivery path takes part, each with a dispatcher, in every region. A second environment, perf, gets an orchestrator of its own, and the same teams’ dispatchers register with it too.', lit: [{ edge: 'to-staging', kind: 'coord', packets: 1, speed: 4 }, { edge: 'to-perf', kind: 'coord', packets: 2, speed: 18 }] },
			{ name: 'The organization', text: 'Several groups, one for each kind of traffic, in every shared environment, each with one orchestrator. No step changed what came before, and any team can still sit out, stood in for by a mock.' },
		],
	},
};

// The lease timeline: two seconds a character, from 0 to 100 s.
const X = (t: number) => 18 + Math.round(t / 2);
const span = (y: number, from: number, to: number, ch: string, kind: Text['kind']): Text => ({ x: X(from), y, text: ch.repeat(Math.max(1, X(to) - X(from) + 1)), kind });
const mark = (y: number, t: number, ch: string, kind: Text['kind']): Text => ({ x: X(t), y, text: ch, kind });
const DOWN = 10, UP = 57, LEASE = 30;
const lanes = { suite: 6, east: 8, west: 10, tracking: 12, billing: 14 };

/** A lane at a moment: its renewals so far, the lease they hold, and what became of it. */
function lane(y: number, now: number, back: boolean): Text[] {
	const out: Text[] = [span(y, 0, Math.min(now, DOWN + LEASE), '─', 'line')];
	if (now > DOWN + LEASE) out.push(mark(y, DOWN + LEASE, '×', 'dim'));
	const renewals = [0, 10, ...(back ? [58, 68, 78, 88, 98] : [])].filter((t) => t <= now);
	const last = renewals[renewals.length - 1];
	if (back && last >= 58) out.push(span(y, 58, Math.min(now, last + LEASE), '─', 'line'));
	for (const t of renewals) out.push(mark(y, t, '•', 'coord'));
	return out;
}
const orchestrator = (now: number): Text[] => [
	span(4, 0, Math.min(now, DOWN), '━', 'ok'),
	...(now > DOWN ? [span(4, DOWN + 2, Math.min(now, UP) - 2, '┄', 'fail')] : []),
	...(now >= UP ? [span(4, UP, now, '━', 'ok')] : []),
];
const restart: Text[] = [3, 5, 7, 9, 11, 13].map((y) => ({ x: X(UP), y, text: '┆', kind: 'label' as const }));
const allLanes = (now: number, back: boolean) => [lanes.suite, lanes.east, lanes.west, lanes.tracking].flatMap((y) => lane(y, now, back));
const note = (y: number, text: string, kind: Text['kind'] = 'label'): Text => ({ x: 71, y, text, kind });

/** What an orchestrator outage does to leases, now that a restart grants one lease of grace. */
export const leaseLapse: OrgDiagram = {
	title: 'Leases across an orchestrator outage',
	text: 'A timeline from 0 to 100 seconds. The suite renews its lease and the dispatchers register every 10 seconds, each renewal holding a 30-second lease. The orchestrator goes down at 10 seconds and comes back at 57. The leases run out at 40 seconds, while it is down, when nobody could renew them. On restart, a lease that lapsed before the orchestrator started gets one lease, to 87 seconds, to be renewed, and silence counts only from the restart: no instance is marked unreachable. The dispatchers register again at 58 seconds, report, and the run carries on and cleans up. An instance that stays silent for a full lease after the restart, to 87 seconds, is unreachable, as it would be at any time.',
	scene: {
		width: 100,
		height: 16,
		texts: [
			...[0, 20, 40, 60, 80].map((t) => ({ x: X(t), y: 1, text: String(t), kind: 'label' as const })),
			{ x: X(100) - 1, y: 1, text: '100 s', kind: 'label' },
			span(2, 0, 100, '─', 'line'),
			...[0, 10, 20, 30, 40, 50, 60, 70, 80, 90, 100].map((t) => mark(2, t, '┼', 'line')),
			{ x: 0, y: 4, text: 'orchestrator', kind: 'title' }, { x: 0, y: lanes.suite, text: 'suite', kind: 'title' },
			{ x: 0, y: lanes.east, text: 'parcels/east', kind: 'title' }, { x: 0, y: lanes.west, text: 'parcels/west', kind: 'title' },
			{ x: 0, y: lanes.tracking, text: 'tracking/shared', kind: 'title' },
			{ x: 0, y: lanes.billing, text: 'billing/shared', kind: 'title', from: 4 },
		],
		steps: [
			{ name: 'Renewing as usual', text: 'The suite renews its lease and every dispatcher registers every 10 s. Each renewal holds a 30 s lease.', texts: [
				...orchestrator(10), ...allLanes(10, false), note(4, 'up', 'ok'), note(lanes.suite, 'renewing every 10 s'), note(lanes.east, 'registering every 10 s'),
			] },
			{ name: 'The orchestrator is down for 47 s', text: 'From 10 s to 57 s nobody can renew anything. Every lease runs out at 40 s, while the orchestrator is down: that silence is the orchestrator’s, not theirs.', texts: [
				...orchestrator(56), ...allLanes(56, false), note(4, 'down for 47 s', 'fail'), note(lanes.suite, 'lease ran out at 40 s', 'dim'), note(lanes.east, 'lease ran out at 40 s', 'dim'), note(lanes.west, 'lease ran out at 40 s', 'dim'), note(lanes.tracking, 'lease ran out at 40 s', 'dim'),
			] },
			{ name: 'It comes back: one lease to renew', text: 'On restart, a lease that lapsed before the orchestrator started gets one lease, to 87 s, to be renewed, and silence counts only from the restart. Nobody is marked unreachable. The dispatchers register again at 58 s and report.', texts: [
				...restart, ...orchestrator(62), ...allLanes(62, true), note(4, 'back at 57 s: one lease', 'ok'), note(5, 'of grace, to 87 s', 'ok'), note(lanes.suite, 'renewing again ✓', 'ok'), note(lanes.east, 'registered again ✓', 'ok'), note(lanes.west, 'registered again ✓', 'ok'), note(lanes.tracking, 'registered again ✓', 'ok'),
			] },
			{ name: 'The run carries on, and cleans up', text: 'Nothing was marked unreachable, so the run carries on where it was. Cleanup runs as it would have, and the run closes clean.', texts: [
				...restart, ...orchestrator(100), ...allLanes(100, true), note(4, 'up', 'ok'), note(lanes.suite, 'run closes clean ✓', 'ok'), note(lanes.east, 'cleaned up ✓', 'own'), note(lanes.west, 'cleaned up ✓', 'own'), note(lanes.tracking, 'cleaned up ✓', 'own'),
			] },
			{ name: 'What still counts', text: 'An instance that stays silent for a full lease after the restart is unreachable, as at any other time: one that never came back holds no run up past 87 s.', texts: [
				...restart, ...orchestrator(100), ...allLanes(100, true), ...lane(lanes.billing, 57, false), mark(lanes.billing, 87, '×', 'fail'), span(lanes.billing, UP + 2, 85, '┄', 'dim'),
				note(4, 'up', 'ok'), note(lanes.billing, 'silent a lease after restart:', 'fail'), note(lanes.billing + 1, 'unreachable at 87 s', 'fail'),
			] },
		],
	},
};

/** Each diagram by its component's name, for the pages' Markdown twins (src/lib/markdown.ts). */
export const ORG_DIAGRAMS: Record<string, OrgDiagram> = {
	AroundTheApps: aroundTheApps,
	OrgEnvironments: orgEnvironments,
	OrgPaths: orgPaths,
	GroupsAndRequired: groupsAndRequired,
	LeaseLapse: leaseLapse,
	Rollout: rollout,
};
