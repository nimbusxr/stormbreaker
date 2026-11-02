/**
 * The topology builder's model (src/components/TopologyBuilder.astro): an environment as a reader
 * describes it, drawn as a scene (src/lib/ascii-diagram.ts) with stormbreaker on top, and the
 * list of what each party sets up. It runs in the browser.
 *
 * Services take part: each has a dispatcher. Dependencies are what they call that does not take
 * part: another team's service in the environment (internal), or something outside it (external).
 * A dependency is mocked, or reached for real by runs. A mock is a proxy too: its stubs answer what
 * they match, and everything else passes through to the real one.
 *
 * The drawing reads left to right, the way traffic goes: the service the load enters by first,
 * then each one it calls after the services that call it; a call back (a cycle) does not move
 * anything. Each zone (a region, an availability zone or a data center) is a box of its own,
 * holding an instance of each service that runs there, with its dispatcher over it; services that
 * run once sit in a shared box. Calls between neighbours go straight across, both ways in one
 * line when they call each other; calls past a service go under the boxes, on tracks that nest.
 * Calls out of a box leave on the right, along a lane per destination, to a dependency or into
 * another box from under it. Dependencies sit right of the boxes, a row each: other teams'
 * services inside the environment, the rest outside it. Once stormbreaker is on top, a mock just
 * inside the cluster's edge stands in front of each mocked dependency.
 */
import type { Box, Edge, Kind, Scene, Step, Text } from './ascii-diagram';

export interface Service {
	name: string;
	/** The zones it runs an instance in, each with a dispatcher instance of its own. None: it
	 * runs once, in no zone of its own. */
	zones: string[];
	/** The services and dependencies it calls, by name. */
	calls: string[];
}
export interface Dependency {
	name: string;
	/** Another team's service in the environment, or something outside it. */
	internal: boolean;
	/** Whether a mock stands in front of it, passing on what its stubs do not match; otherwise runs reach it for real. */
	mocked: boolean;
}
export interface Topology {
	environment: string;
	/** Regions, availability zones or data centers: wherever a service runs an instance of its own. */
	zones: string[];
	group: string;
	services: Service[];
	dependencies: Dependency[];
	metrics: 'per-app' | 'per-zone' | 'global';
	/** The service the suite sends load to. */
	target: string;
}

export const EXAMPLE: Topology = {
	environment: 'staging',
	zones: ['east', 'west'],
	group: 'delivery',
	services: [
		{ name: 'parcels', zones: ['east', 'west'], calls: ['tracking', 'billing'] },
		{ name: 'tracking', zones: [], calls: ['courier'] },
	],
	dependencies: [
		{ name: 'billing', internal: true, mocked: true },
		{ name: 'courier', internal: false, mocked: true },
	],
	metrics: 'per-zone',
	target: 'parcels',
};

export const LIMITS = { services: 8, dependencies: 5, zones: 3 };

const BOX = 18; // a service's width
const GAP = 6; // between two services: room for a call and its arrows
const SLOT = 5; // a dependency's height and the row after it
const RUNNER = 20;
const ENV = RUNNER + 3; // the environment's left rule
const SUITE = ENV + 2; // the suite's line down the left: its load, and its checks
const TRUNK = ENV + 4; // the orchestrator's line down the left, to the bus over each box of dispatchers
const BAND = ENV + 6; // the zones' left rule
const X0 = BAND + 3; // the first column

const cut = (s: string, n: number) => (s.length > n ? s.slice(0, n - 1) + '…' : s);
export const slug = (s: string) => (s ?? '').trim().toLowerCase().replace(/[^a-z0-9-]+/g, '-').replace(/^-+|-+$/g, '');

/** A topology from anything saved or shared, older shapes included: teams that sat out became
 * internal dependencies, partners external ones, regions zones, and a service in every zone one
 * with every zone ticked. */
// eslint-disable-next-line @typescript-eslint/no-explicit-any
export function normalize(raw: any): Topology {
	const r = raw ?? {};
	const legacy = Array.isArray(r.teams);
	const zones: string[] = r.zones ?? r.regions ?? [];
	const zonesOf = (m: any): string[] => (Array.isArray(m.zones) ? m.zones : (m.perZone ?? m.regional) ? zones : []);
	const services: Service[] = (legacy ? r.teams.filter((m: any) => m.takesPart !== false) : (r.services ?? []))
		.map((m: any) => ({ name: m.name, zones: zonesOf(m), calls: m.calls ?? [] }));
	const dependencies: Dependency[] = legacy
		? [
			...r.teams.filter((m: any) => m.takesPart === false).map((m: any) => ({ name: m.name, internal: true, mocked: true })),
			...(r.partners ?? []).map((p: string) => ({ name: p, internal: false, mocked: true })),
		]
		: (r.dependencies ?? []);
	return {
		environment: r.environment ?? EXAMPLE.environment,
		zones,
		group: r.group ?? EXAMPLE.group,
		services,
		dependencies,
		metrics: r.metrics === 'per-region' ? 'per-zone' : r.metrics,
		target: r.target ?? '',
	};
}

/** The topology, tidied: names slugged, duplicates and unknown calls dropped, within limits. */
export function tidy(input: Topology): Topology {
	const t = normalize(input);
	const zones = [...new Set(t.zones.map(slug).filter(Boolean))].slice(0, LIMITS.zones);
	const seen = new Set<string>();
	const services = t.services
		.map((m) => ({ name: slug(m.name), zones: [...new Set((m.zones ?? []).map(slug))].filter((z) => zones.includes(z)), calls: [...(m.calls ?? [])] }))
		.filter((m) => m.name && !seen.has(m.name) && seen.add(m.name))
		.slice(0, LIMITS.services);
	const dependencies = t.dependencies
		.map((d) => ({ name: slug(d.name), internal: !!d.internal, mocked: !!d.mocked }))
		.filter((d) => d.name && !seen.has(d.name) && seen.add(d.name))
		.slice(0, LIMITS.dependencies);
	const known = new Set([...services.map((m) => m.name), ...dependencies.map((d) => d.name)]);
	for (const m of services) m.calls = [...new Set(m.calls.map(slug))].filter((c) => known.has(c) && c !== m.name);
	const target = services.some((m) => m.name === slug(t.target)) ? slug(t.target) : services[0]?.name ?? '';
	const metrics = t.metrics === 'per-app' || t.metrics === 'global' ? t.metrics : 'per-zone';
	return { environment: slug(t.environment) || 'staging', group: slug(t.group) || 'delivery', zones, services, dependencies, target, metrics };
}

type Point = [number, number];

/** A path without repeated corners, or corners that do not turn. */
function clean(path: Point[]): Point[] {
	const out: Point[] = [];
	for (const p of path) {
		const q = out[out.length - 1], o = out[out.length - 2];
		if (q && q[0] === p[0] && q[1] === p[1]) continue;
		if (o && q && ((o[0] === q[0] && q[0] === p[0]) || (o[1] === q[1] && q[1] === p[1]))) out.pop();
		out.push(p);
	}
	return out;
}

/** The services left to right, the way traffic goes: the one the load enters by, then each one a
 * column after the services that call it. A call back to a service already on the way, a cycle,
 * does not move it. */
function orderOf(t: Topology): string[] {
	const names = t.services.map((m) => m.name);
	const calls = new Map(t.services.map((m) => [m.name, m.calls.filter((c) => c !== t.target && names.includes(c))]));
	const forward = new Map<string, string[]>(names.map((n) => [n, []]));
	const state = new Map<string, 'open' | 'done'>();
	const visit = (n: string) => {
		state.set(n, 'open');
		for (const c of calls.get(n)!) {
			if (state.get(c) === 'open') continue;
			forward.get(n)!.push(c);
			if (!state.has(c)) visit(c);
		}
		state.set(n, 'done');
	};
	for (const n of [t.target, ...names]) if (calls.has(n) && !state.has(n)) visit(n);
	const layer = new Map(names.map((n) => [n, 0]));
	for (let i = 0; i < names.length; i++) for (const [a, cs] of forward) for (const c of cs) layer.set(c, Math.max(layer.get(c)!, layer.get(a)! + 1));
	const order: string[] = [];
	for (let l = 0; l <= Math.max(0, ...layer.values()); l++) {
		// Within a column's worth, each near the services that call it.
		const near = (n: string) => {
			const at = names.filter((p) => forward.get(p)!.includes(n)).map((p) => order.indexOf(p));
			return at.length ? at.reduce((s, v) => s + v, 0) / at.length : 0;
		};
		order.push(...names.filter((n) => layer.get(n) === l).sort((a, b) => Number(b === t.target) - Number(a === t.target) || near(a) - near(b) || names.indexOf(a) - names.indexOf(b)));
	}
	return order;
}

/** The scene: the environment as it is, then stormbreaker on top, a run, its checks, and cleanup. */
export function scene(input: Topology): Scene {
	const t = tidy(input);
	const boxes: Box[] = [], edges: Edge[] = [], texts: Text[] = [];
	const service = new Map(t.services.map((m) => [m.name, m]));
	const dependency = new Map(t.dependencies.map((d) => [d.name, d]));
	const internal = t.dependencies.filter((d) => d.internal), external = t.dependencies.filter((d) => !d.internal);
	const mocked = t.dependencies.filter((d) => d.mocked), reachedForReal = t.dependencies.filter((d) => !d.mocked);
	const perApp = t.metrics === 'per-app', perZone = t.metrics === 'per-zone', global = t.metrics === 'global';

	// The services in the order traffic goes; each box holds its own, packed left to right.
	const order = orderOf(t);

	// Bands: a box per zone, holding an instance of each service that runs there, and one for the
	// services that run once. A zone nobody runs in is not drawn.
	type Band = { id: string; title: string; zone?: string; members: string[] };
	const zoned = t.services.filter((m) => m.zones.length), once = t.services.filter((m) => !m.zones.length);
	const bands: Band[] = [
		...t.zones.map((z) => ({ id: `z-${z}`, title: `zone ${z}`, zone: z, members: order.filter((n) => service.get(n)!.zones.includes(z)) })).filter((b) => b.members.length),
		...(once.length ? [{ id: 'shared', title: zoned.length ? 'shared' : `${t.environment}, in one place`, members: order.filter((n) => !service.get(n)!.zones.length) }] : []),
	];
	const bandOf = new Map(bands.map((b) => [b.id, b]));
	type Inst = { name: string; band: Band };
	const keyOf = (i: Inst) => `${i.name}@${i.band.id}`;
	const column = (i: Inst) => i.band.members.indexOf(i.name);
	const xOf = (i: Inst) => X0 + column(i) * (BOX + GAP);
	const right = X0 + Math.max(0, ...bands.map((b) => b.members.length - 1)) * (BOX + GAP) + BOX + 2;
	// A call stays in its zone when the callee runs there too; otherwise it goes to every instance
	// the callee has, in its zones or in no zone at all.
	const instancesOf = (name: string, from: Band): Inst[] => {
		const m = service.get(name)!;
		if (!m.zones.length) return [{ name, band: bandOf.get('shared')! }];
		if (from.zone && m.zones.includes(from.zone)) return [{ name, band: from }];
		return m.zones.map((z) => ({ name, band: bandOf.get(`z-${z}`)! }));
	};

	// Calls between neighbours in a box go straight across; calls past a service, under the
	// boxes, on a track of their own. Calls out of a box (to a dependency, or to another box)
	// leave by the right side, or under the boxes when something is right of the caller; they come
	// into another box from the right, under it.
	type Arc = { band: Band; span: number; ports: Map<string, number>; lo: number; hi: number; track: number };
	const arcs: Arc[] = [];
	const ends = new Map<string, { arc: Arc; side: 'L' | 'R'; x: number }[]>();
	const arcOf = (left: Inst, other?: Inst): Arc => {
		const span = other ? column(other) - column(left) : 1000 - column(left);
		const arc: Arc = { band: left.band, span, ports: new Map(), lo: 0, hi: 0, track: 0 };
		arcs.push(arc);
		for (const [i, side] of [[left, 'R'], ...(other ? [[other, 'L']] : [])] as [Inst, 'L' | 'R'][]) {
			const k = keyOf(i);
			ends.set(k, [...(ends.get(k) ?? []), { arc, side, x: xOf(i) }]);
		}
		return arc;
	};
	type Pair = { band: Band; a: string; b: string; ab: boolean; ba: boolean; arc?: Arc };
	type Out = { from: Inst; inst?: Inst; dep?: Dependency };
	const pairs = new Map<string, Pair>(), outs: Out[] = [];
	for (const b of bands) for (const name of b.members) {
		const from = { name, band: b };
		for (const c of service.get(name)!.calls) {
			const dep = dependency.get(c);
			if (dep) { outs.push({ from, dep }); continue; }
			for (const to of instancesOf(c, b)) {
				if (to.band !== b) { outs.push({ from, inst: to }); continue; }
				const [a, z] = column(from) < column(to) ? [name, c] : [c, name];
				const k = `${b.id}:${a}:${z}`;
				const p = pairs.get(k) ?? { band: b, a, b: z, ab: false, ba: false };
				if (a === name) p.ab = true; else p.ba = true;
				pairs.set(k, p);
			}
		}
	}
	for (const p of pairs.values()) {
		const a = { name: p.a, band: p.band }, b = { name: p.b, band: p.band };
		if (column(b) - column(a) > 1) p.arc = arcOf(a, b);
	}
	const exitArc = new Map<string, Arc>(), entryArc = new Map<string, Arc>();
	for (const o of outs) {
		const k = keyOf(o.from), members = o.from.band.members;
		if (o.from.name !== members[members.length - 1] && !exitArc.has(k)) exitArc.set(k, arcOf(o.from));
		if (o.inst && !entryArc.has(keyOf(o.inst))) entryArc.set(keyOf(o.inst), arcOf(o.inst));
	}
	// Where each track meets a box: the longest in the middle, so that tracks nest and do not cross.
	for (const [k, list] of ends) {
		const all = [...list.filter((e) => e.side === 'L').sort((p, q) => p.arc.span - q.arc.span), ...list.filter((e) => e.side === 'R').sort((p, q) => q.arc.span - p.arc.span)];
		const sp = all.length <= 5 ? 3 : 2, start = list[0].x + Math.round(BOX / 2 - ((all.length - 1) * sp) / 2);
		all.forEach((e, i) => e.arc.ports.set(k, start + i * sp));
	}
	const tracks = new Map<string, number>();
	for (const b of bands) {
		const mine = arcs.filter((a) => a.band === b);
		for (const a of mine) {
			const ps = [...a.ports.values()];
			a.lo = Math.min(...ps);
			a.hi = ps.length === 2 ? Math.max(...ps) : right;
		}
		// The shortest nearest the boxes; of two from one box, the one further right.
		mine.sort((p, q) => p.span - q.span || q.lo - p.lo);
		const used: [number, number][][] = [];
		for (const a of mine) {
			let n = 0;
			while (used[n]?.some(([lo, hi]) => a.lo <= hi + 1 && lo <= a.hi + 1)) n++;
			(used[n] ??= []).push([a.lo, a.hi]);
			a.track = n;
		}
		tracks.set(b.id, used.length);
	}

	// Each box, top to bottom: the bus from the orchestrator, the dispatchers, each reaching its
	// service through the team's own door, the services, the tracks under them, and Prometheus.
	type Geo = { y: number; service: number; leg: number; prom?: number; bottom: number };
	const geo = new Map<string, Geo>();
	const zoneProm = (b: Band) => perZone && (b.zone !== undefined || !zoned.length);
	let y = 6;
	for (const b of bands) {
		const n = tracks.get(b.id)!, service = y + 7, leg = service + 4;
		const after = n ? leg + n + 2 : leg + 1;
		const prom = perApp || zoneProm(b) ? after : undefined;
		const bottom = prom === undefined ? after : after + 4;
		geo.set(b.id, { y, service, leg, prom, bottom });
		boxes.push({ id: b.id, x: BAND, y, w: right - BAND + 1, h: bottom - y + 1, title: b.title, style: 'zone' });
		y = bottom + 2;
	}
	const trackY = (a: Arc) => geo.get(a.band.id)!.leg + 1 + a.track;

	// Dependencies, right of the boxes, a row each: mocks just inside the cluster's edge, other
	// teams' services beside them, and what is outside the environment beyond its edge.
	const exitRow = (o: Out) => { const a = exitArc.get(keyOf(o.from)); return a ? trackY(a) : geo.get(o.from.band.id)!.service + 1; };
	// Each level with the first box's services, so that a call to it can go straight across; but
	// not on a row where a line leaves for a dependency further up, past the lane that brings
	// other callers' lines to this one, or the two would share a stretch.
	const deps = [...mocked, ...reachedForReal];
	const depRow = new Map<Dependency, number>();
	let next = bands.length ? geo.get(bands[0].id)!.service : 8;
	for (const d of deps) {
		const callers = new Set(outs.filter((o) => o.dep === d).map((o) => keyOf(o.from)));
		const above = new Set(deps.slice(0, deps.indexOf(d)));
		const busy = new Set(outs.filter((o) => o.dep && above.has(o.dep) && [...callers].some((k) => k !== keyOf(o.from))).map(exitRow));
		while (busy.has(next + 1)) next++;
		depRow.set(d, next);
		next += SLOT;
	}
	const top = deps.length ? depRow.get(deps[0])! : 8;
	const depY = (d: Dependency) => depRow.get(d)!;
	const destRow = (o: Out) => (o.dep ? depY(o.dep) + 1 : trackY(entryArc.get(keyOf(o.inst!))!));
	const laneKey = (o: Out) => (o.dep ? `dep:${o.dep.name}` : `in:${keyOf(o.inst!)}`);
	// Lanes, between the boxes and the dependencies: one for each place lines go, so that lines
	// to the same place merge, and lines to different places cross rather than share a stretch.
	const needed = new Map<string, number>();
	for (const o of outs) if (o.inst || exitRow(o) !== destRow(o)) needed.set(laneKey(o), destRow(o));
	// Dependencies' lanes nest so that the highest is furthest out, which keeps a line leaving
	// level with one dependency off the line arriving there. When no line does, as when they all
	// rise from below, nesting the other way crosses less.
	const tangles = (order: string[]) => {
		const runs = outs.filter((o) => order.includes(laneKey(o))).map((o) => ({ from: keyOf(o.from), at: order.indexOf(laneKey(o)), e: exitRow(o), d: destRow(o) }));
		const within = (y: number, r: { e: number; d: number }) => y > Math.min(r.e, r.d) && y < Math.max(r.e, r.d);
		let n = 0;
		for (const a of runs) for (const b of runs) {
			if (a.at >= b.at) continue; // a's lane is left of b's: a goes on past b's, and b comes from past a's
			if (within(a.d, b)) n++;
			if (within(b.e, a)) n++;
			if (a.from !== b.from && (a.d === b.e || a.e === b.e)) n += 1000; // a shared stretch
		}
		return n;
	};
	const depLanes = deps.map((d) => `dep:${d.name}`).filter((k) => needed.has(k)).reverse();
	const nested = [...depLanes].reverse();
	const lanes = [
		...[...needed].filter(([k]) => k.startsWith('in:')).sort((p, q) => p[1] - q[1]).map(([k]) => k),
		...(tangles(nested) < tangles(depLanes) ? nested : depLanes),
	];
	const laneX = new Map(lanes.map((k, i) => [k, right + 2 + 2 * i]));
	const laneEnd = right + 2 * lanes.length;
	const mockX = laneEnd + 5;
	const intX = mocked.length ? mockX + BOX + 4 : laneEnd + 3;
	const envRight = internal.length ? intX + BOX + 2 : mocked.length ? mockX + BOX + 3 : laneEnd + 3;
	const outX = envRight + 5;
	const lastBottom = bands.length ? geo.get(bands[bands.length - 1].id)!.bottom : 6;
	const promY = lastBottom + 2;
	const height = Math.max(lastBottom, deps.length ? depY(deps[deps.length - 1]) + 5 : 0, global ? promY + 3 : 0) + 2;
	const width = external.length ? outX + BOX + 3 : envRight + 2;

	boxes.push({ id: 'env', x: ENV, y: 0, w: envRight - ENV + 1, h: height, title: t.environment, style: 'zone' });
	boxes.push({ id: 'runner', x: 0, y: 1, w: RUNNER, h: 5, title: 'CI runner', style: 'heavy', lines: ['the suite', cut(`load: ${t.target}`, RUNNER - 4)], from: 1 });
	boxes.push({ id: 'orch', x: X0, y: 1, w: Math.min(34, envRight - X0 - 1), h: 4, title: 'orchestrator', style: 'heavy', lines: ['one for the environment', cut(`group: ${t.group}`, 30)], from: 1 });

	// Each service, with its dispatcher over it: a process of its own, never inside it.
	const dispatchers: string[] = [], doors: string[] = [], control: string[] = [], targets: string[] = [];
	const proms: { id: string; y: number }[] = [];
	for (const b of bands) {
		const g = geo.get(b.id)!;
		for (const name of b.members) {
			const k = `${name}@${b.id}`, bx = xOf({ name, band: b }), cx = bx + BOX / 2;
			boxes.push({ id: `${k}+`, x: bx, y: g.service, w: BOX, h: 4, title: cut(name, BOX - 4), inside: true, lines: [b.zone ?? 'one instance'] });
			boxes.push({ id: `d-${k}`, x: bx + 2, y: g.y + 3, w: BOX - 4, h: 3, title: 'dispatcher', inside: true, from: 1 });
			edges.push({ id: `door-${k}`, path: [[cx, g.y + 6]], dir: 'down', from: 1 });
			// The orchestrator and the dispatcher: notifications down, reads and reports up.
			edges.push({ id: `n-${k}`, path: [[X0 - 1, 3], [TRUNK, 3], [TRUNK, g.y + 1], [cx, g.y + 1], [cx, g.y + 2]], twoWay: true, from: 1 });
			dispatchers.push(`d-${k}`);
			doors.push(`door-${k}`);
			control.push(`n-${k}`);
			if (name === t.target) targets.push(`${k}+`);
			if (perApp) {
				boxes.push({ id: `p-${k}`, x: bx, y: g.prom!, w: BOX, h: 3, title: 'prometheus', inside: true });
				if (name === t.target) proms.push({ id: `p-${k}`, y: g.prom! });
			}
		}
		if (zoneProm(b)) {
			boxes.push({ id: `p-${b.id}`, x: X0, y: g.prom!, w: BOX, h: 3, title: 'prometheus', inside: true });
			proms.push({ id: `p-${b.id}`, y: g.prom! });
		}
	}
	if (global) {
		boxes.push({ id: 'prom', x: X0, y: promY, w: BOX, h: 4, title: 'prometheus', inside: true, lines: ['one global view'] });
		proms.push({ id: 'prom', y: promY });
	}
	if (mocked.length) {
		boxes.push({ id: 'mocks', x: mockX - 2, y: top - 2, w: BOX + 4, h: depY(mocked[mocked.length - 1]) - top + 8, title: 'mocks', style: 'zone', from: 1 });
		for (const d of mocked) boxes.push({ id: `m-${d.name}`, x: mockX, y: depY(d), w: BOX, h: 4, title: cut(`${d.name} mock`, BOX - 4), inside: true, lines: ['a stand-in'], from: 1 });
	}
	for (const d of internal) {
		const title = cut(d.name, BOX - 4);
		boxes.push({ id: `i-${d.name}`, x: intX, y: depY(d), w: BOX, h: 4, title, inside: true, lines: ['another team'], until: 0 });
		boxes.push({ id: `i-${d.name}+`, x: intX, y: depY(d), w: BOX, h: 4, title, inside: true, lines: [d.mocked ? 'gets the rest' : 'no dispatcher'], from: 1, ...(d.mocked ? { dimFrom: 1 } : {}) });
	}
	if (external.length) {
		const ys = external.map(depY), y0 = Math.min(...ys), y1 = Math.max(...ys);
		boxes.push({ id: 'outside', x: outX - 2, y: y0 - 2, w: BOX + 4, h: y1 - y0 + 8, title: 'outside', style: 'zone' });
		for (const d of external) {
			const title = cut(d.name, BOX - 4);
			boxes.push({ id: `x-${d.name}`, x: outX, y: depY(d), w: BOX, h: 4, title, inside: true, lines: ['outside'], until: 0 });
			boxes.push({ id: `x-${d.name}+`, x: outX, y: depY(d), w: BOX, h: 4, title, inside: true, lines: [d.mocked ? 'gets the rest' : 'reached by runs'], from: 1, ...(d.mocked ? { dimFrom: 1 } : {}) });
		}
	}

	// Calls: to the service or dependency itself, as it is; once stormbreaker is on top, to the
	// mock of a mocked dependency.
	const traffic: string[] = [], reached = new Set<string>();
	let n = 0;
	for (const p of pairs.values()) {
		const g = geo.get(p.band.id)!, ka = `${p.a}@${p.band.id}`, kb = `${p.b}@${p.band.id}`;
		const path: Point[] = p.arc
			? [[p.arc.ports.get(ka)!, g.leg], [p.arc.ports.get(ka)!, trackY(p.arc)], [p.arc.ports.get(kb)!, trackY(p.arc)], [p.arc.ports.get(kb)!, g.leg]]
			: [[xOf({ name: p.a, band: p.band }) + BOX, g.service + 1], [xOf({ name: p.b, band: p.band }) - 1, g.service + 1]];
		if (!p.ab) path.reverse();
		const id = `c${n++}`;
		edges.push({ id, path, ...(p.ab && p.ba ? { twoWay: true } : {}) });
		traffic.push(id);
		if (p.ab) reached.add(`${kb}+`);
		if (p.ba) reached.add(`${ka}+`);
	}
	for (const o of outs) {
		const g = geo.get(o.from.band.id)!, k = keyOf(o.from), ex = exitArc.get(k);
		const start: Point[] = ex ? [[ex.ports.get(k)!, g.leg], [ex.ports.get(k)!, trackY(ex)]] : [[xOf(o.from) + BOX, g.service + 1]];
		const lx = laneX.get(laneKey(o));
		const via = (...end: Point[]) => clean([...start, ...(lx === undefined ? [] : [[lx, exitRow(o)], [lx, destRow(o)]] as Point[]), ...end]);
		if (o.inst) {
			const ek = keyOf(o.inst), pe = entryArc.get(ek)!.ports.get(ek)!;
			const id = `c${n++}`;
			edges.push({ id, path: via([pe, destRow(o)], [pe, geo.get(o.inst.band.id)!.leg]) });
			traffic.push(id);
			reached.add(`${ek}+`);
			continue;
		}
		const d = o.dep!, id = `c${n++}`;
		edges.push({ id, path: via([(d.internal ? intX : outX) - 1, destRow(o)]), ...(d.mocked ? { until: 0 } : {}) });
		if (d.mocked) {
			const mid = `c${n++}`;
			edges.push({ id: mid, path: via([mockX - 1, destRow(o)]), from: 1 });
			traffic.push(mid);
			reached.add(`m-${d.name}`);
		} else {
			traffic.push(id);
			reached.add(d.internal ? `i-${d.name}+` : `x-${d.name}+`);
		}
	}
	// What a mock's stubs do not match passes through to the real one.
	for (const d of mocked) edges.push({ id: `pass-${d.name}`, path: [[mockX + BOX, depY(d) + 1], [(d.internal ? intX : outX) - 1, depY(d) + 1]], from: 1 });

	// The suite: to the orchestrator, to the service the load enters by, and to Prometheus, down
	// one line on the left.
	edges.push({ id: 'suite', path: [[RUNNER, 2], [X0 - 1, 2]], from: 1 });
	const entries = bands.filter((b) => b.members.includes(t.target)).map((b) => geo.get(b.id)!.service);
	entries.forEach((s, i) => edges.push({ id: `load${i}`, path: [[RUNNER, 4], [SUITE, 4], [SUITE, s + 1], [X0 - 1, s + 1]], from: 1 }));
	proms.forEach((p, i) => edges.push({ id: `judge${i}`, path: [[RUNNER, 4], [SUITE, 4], [SUITE, p.y + 1], [X0 - 1, p.y + 1]], from: 1 }));
	const asked = perApp ? `${t.target}'s own Prometheus in each zone` : perZone ? "each zone's Prometheus" : 'the global view';
	const lit = (list: string[], kind: Kind, reverse = false) => list.map((edge) => ({ edge, kind, ...(reverse ? { reverse } : {}) }));

	const names = (list: { name: string }[]) => list.map((d) => d.name).join(', ');
	const steps: Step[] = [
		{
			name: 'As it is',
			text: `${t.environment}: ${t.services.length} ${t.services.length === 1 ? 'service' : 'services'}${zoned.length ? ` across ${t.zones.length === 1 ? 'one zone' : `${t.zones.length} zones`}` : ''}${t.dependencies.length ? `, depending on ${names(t.dependencies)}` : ''}. None of it changes.`,
		},
		{
			name: 'Stormbreaker on top',
			text: `An orchestrator for ${t.environment}, and a dispatcher beside each service: a process of its own, so the services and their pods do not change${mocked.length ? `. Mocks in the cluster stand in for ${names(mocked)}: their stubs answer the run, and everything else passes through` : ''}${reachedForReal.length ? `; runs reach ${names(reachedForReal)} for real` : ''}.`,
		},
		{
			name: 'A run opens',
			text: `The suite opens a run in ${t.group}. The orchestrator writes it down, then notifies every dispatcher in the group: run.opened.`,
			lit: [{ edge: 'suite', kind: 'coord' }, ...lit(control, 'notify')],
			marks: dispatchers.map((box) => ({ box, kind: 'notify' as Kind })),
		},
		{
			name: 'Prepare',
			text: 'The suite declares what its load will create. Each dispatcher reads the run, declares what its own service will create from it, prepares, and reports. A report that grows the footprint notifies every dispatcher again (run.footprint-updated), and rounds go on until one adds nothing.',
			lit: [{ edge: 'suite', kind: 'coord' }, ...lit(control, 'coord', true)],
			marks: dispatchers.map((box) => ({ box, kind: 'coord' as Kind })),
		},
		{
			name: 'The load',
			text: `Every service has prepared on the footprint as it is, so the readiness gate opens, and the load goes to ${t.target} through its front door, and on to what it calls${mocked.length ? ', mocks answering for the mocked dependencies' : ''}.`,
			lit: [...lit(entries.map((_, i) => `load${i}`), 'traffic'), ...lit(traffic, 'traffic')],
			marks: [...new Set([...targets, ...reached])].map((box) => ({ box, kind: 'traffic' as Kind })),
		},
		{
			name: 'Its checks',
			text: `The suite judges its SLOs from ${asked}, over the run's window${proms.length > 1 ? ': counters summed and histogram buckets merged across them, never averaged' : ''}.`,
			lit: lit(proms.map((_, i) => `judge${i}`), 'coord'),
			marks: proms.map((p) => ({ box: p.id, kind: 'coord' as Kind })),
		},
		{
			name: 'Cleanup',
			text: "The suite says its traffic is done and the run has settled. The orchestrator notifies every dispatcher: run.cleanup-ready. Each removes what its own service created, through its team's own door, and checks it is gone.",
			lit: [{ edge: 'suite', kind: 'coord' }, ...lit(control, 'notify'), ...lit(doors, 'own')],
			marks: dispatchers.map((box) => ({ box, kind: 'own' as Kind })),
		},
		{
			name: 'Closed',
			text: `Each dispatcher reports what it removed, and that it is done. When every one has, the orchestrator closes the run, and its journal tells the whole story.${internal.some((d) => !d.mocked) ? ` Nothing cleaned up ${names(internal.filter((d) => !d.mocked))}, which ${internal.filter((d) => !d.mocked).length === 1 ? 'has' : 'have'} no dispatcher.` : ''}`,
			lit: lit(control, 'own', true),
			marks: dispatchers.map((box) => ({ box, kind: 'own' as Kind })),
		},
	];
	return { width, height, boxes, edges, texts, steps };
}

/** What each party sets up, in words. */
export function checklist(input: Topology): { who: string; items: string[] }[] {
	const t = tidy(input);
	const mocked = t.dependencies.filter((d) => d.mocked);
	const promUrl = t.metrics === 'per-app'
		? `the ${t.target} team's Prometheus in each zone (${t.zones.join(', ')}), separated by |`
		: t.metrics === 'per-zone' ? `each zone's Prometheus (${t.zones.join(', ')}), separated by |` : 'the global view (Thanos, Mimir or a central Prometheus)';
	const out: { who: string; items: string[] }[] = [
		{
			who: 'The platform team',
			items: [
				`Deploy one orchestrator in ${t.environment}, reachable from the CI runner and from every service's dispatcher.`,
				...(mocked.length ? [`Run a mock in the cluster for ${mocked.map((d) => d.name).join(', ')}, with axx-wiremock, a mock image of its stubs, and --proxy-all set to the real one, so that what the stubs do not match passes through.`] : []),
			],
		},
	];
	for (const m of t.services) {
		const instances = m.zones.length ? m.zones.map((z) => `${m.name}/${z}`) : [`${m.name}/shared`];
		out.push({
			who: `The ${m.name} team`,
			items: [
				`Run a dispatcher, ${instances.length === 1 ? 'one instance' : `an instance in each of its zones`} (${instances.join(', ')}), in group ${t.group}.`,
				`Prepare: declare what ${m.name} will create from what the run declared. Cleanup: remove it through your own door, and prove it.`,
				...m.calls.filter((c) => mocked.some((d) => d.name === c)).map((c) => `Point your calls to ${c} at the ${c} mock, and leave them there: what its stubs do not match passes through to ${c}.`),
			],
		});
	}
	for (const d of t.dependencies.filter((x) => x.internal)) {
		out.push({
			who: `The ${d.name} team`,
			items: [d.mocked
				? `Nothing: the mock's stubs answer the run, and everything else still reaches ${d.name}. It can take part later, with a dispatcher.`
				: `Runs reach ${d.name}. If it keeps anything because of them, it needs a dispatcher, and should take part; if not, nothing.`],
		});
	}
	const outside = t.dependencies.filter((x) => !x.internal && !x.mocked);
	if (outside.length) {
		out.push({
			who: 'Outside the environment',
			items: outside.map((d) => `Runs reach ${d.name} for real: make sure it is a sandbox that can take the load, and that nothing it keeps matters.`),
		});
	}
	out.push({
		who: 'The suite',
		items: [
			`Tag scenarios @stormbreaker-group/${t.group}${t.services.length ? ` @stormbreaker-requires/${t.services.map((m) => m.name).join(',')}` : ''}.`,
			`Send load to ${t.target} with stormbreaker-rest or stormbreaker-kafka, at a declared rate.`,
			`Judge SLOs with a Prometheus service whose url is ${promUrl}${t.metrics === 'per-app' ? '' : `, with selector app="${t.target}"`}.`,
		],
	});
	return out;
}
