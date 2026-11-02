// An environment's topology as it is, drawn with the docs' diagram engine: nothing here is
// described by hand. The orchestrator and each group's dispatchers, with every instance's lease;
// and, while a run goes, the suite, the services its load goes to, and each instance's part in
// the run: notified, preparing, prepared, cleaning up, done.
import { render, type Box, type Edge, type Kind, type Scene, type Text } from '../../docs/src/lib/ascii-diagram';
import { rate } from './format';
import type { Dispatcher, Instance, LiveRun } from './types';

const BOX = 24; // a dispatcher's width
const GAP = 2;
const cut = (s: string, n: number) => (s.length > n ? s.slice(0, n - 1) + '…' : s);

/** The kind an instance's state is drawn in. */
export function kindOf(state: string): Kind {
	switch (state) {
		case 'prepared': case 'completed': return 'ok';
		case 'unreachable': case 'blocked': return 'fail';
		case 'notified': case 'acknowledged': case 'in-progress': case 'partial': return 'coord';
		default: return 'label';
	}
}

/** One group, drawn: its dispatchers under the orchestrator, and while run goes, the suite and
 * the services under its load. */
export function groupScene(env: string, group: string, dispatchers: Dispatcher[], live?: LiveRun): Scene {
	const boxes: Box[] = [], edges: Edge[] = [], texts: Text[] = [];
	const lit: { edge: string; kind: Kind; reverse?: boolean }[] = [], marks: { box: string; kind: Kind }[] = [];
	const names = [...new Set(dispatchers.map((d) => d.name))];
	const onRun = new Map((live?.run.dispatchers ?? []).map((i) => [`${i.name}/${i.instance}`, i] as const));
	const loads = (live?.progress?.load ?? []).filter((l) => l.target);
	const targets = [...new Set(loads.map((l) => l.target))];
	const now = Date.now();

	// The suite, and the services its load goes to.
	let gx = 0;
	if (live) {
		const p = live.progress;
		const steps = p?.steps ?? [];
		const current = steps.findIndex((s) => s.status === 'running');
		const done = steps.filter((s) => s.status && s.status !== 'running').length;
		boxes.push({ id: 'suite', x: 0, y: 6, w: BOX, h: 5, title: 'the suite', style: 'heavy', lines: [
			cut(p?.scenario ?? live.run.initiator, BOX - 4),
			steps.length ? (current >= 0 ? `step ${current + 1} of ${steps.length}` : `${done} of ${steps.length} steps`) : live.run.state,
		] });
		marks.push({ box: 'suite', kind: 'coord' });
		targets.forEach((t, i) => {
			const y = 7 + i * 6, ls = loads.filter((l) => l.target === t);
			const going = ls.some((l) => !l.done);
			const sent = ls.reduce((n, l) => n + l.sent, 0), planned = ls.reduce((n, l) => n + l.planned, 0);
			boxes.push({ id: `svc-${t}`, x: BOX + 6, y, w: BOX - 2, h: 4, title: cut(t, BOX - 6), inside: true, lines: [going ? `${rate(ls.reduce((n, l) => n + l.rate, 0))}/s declared` : `${sent.toLocaleString('en-US')} of ${planned.toLocaleString('en-US')}`] });
			const path: [number, number][] = y + 1 === 8 ? [[BOX, 8], [BOX + 5, 8]] : [[BOX, 8], [BOX + 3, 8], [BOX + 3, y + 1], [BOX + 5, y + 1]];
			edges.push({ id: `load-${t}`, path });
			if (going) {
				lit.push({ edge: `load-${t}`, kind: 'traffic' });
				marks.push({ box: `svc-${t}`, kind: 'traffic' });
			}
		});
		gx = BOX * 2 + 10;
	}

	// The orchestrator over the group, and its line to every dispatcher: notifications down,
	// reports up.
	const cols = Math.max(1, names.length);
	const w = 4 + cols * (BOX + GAP);
	const trunk = Math.max(gx + 19, gx + group.length + 9);
	const state = live ? (live.run.failed ? 'failed' : live.run.abandoned ? 'abandoned' : live.run.state === 'opened' ? (live.run.gate.open ? 'traffic' : 'preparing') : live.run.state) : '';
	boxes.push({ id: 'orch', x: trunk - 17, y: 0, w: 34, h: 4, title: 'orchestrator', style: 'heavy', lines: [cut(env, 30), live ? cut(`${live.run.id} · ${state}`, 30) : 'no run in this group'] });
	const instancesOf = (name: string) => dispatchers.filter((d) => d.name === name);
	const tallest = Math.max(1, ...names.map((n) => instancesOf(n).length));
	boxes.push({ id: `g-${group}`, x: gx, y: 5, w, h: 6 + tallest + 2, title: group, style: 'zone' });
	names.forEach((name, i) => {
		const x = gx + 2 + i * (BOX + GAP), cx = x + Math.floor(BOX / 2);
		const id = `d-${name}`;
		const insts = instancesOf(name);
		boxes.push({ id, x, y: 8, w: BOX, h: 3 + insts.length, title: cut(name, BOX - 4), inside: true });
		edges.push({ id: `n-${name}`, path: [[trunk, 4], [trunk, 6], [cx, 6], [cx, 7]], twoWay: true });
		const states: string[] = [];
		insts.forEach((d, j) => {
			const leased = Date.parse(d.leaseExpiresAt) > now;
			const inRun: Instance | undefined = onRun.get(`${d.name}/${d.instance}`);
			const what = inRun ? inRun.state : leased ? 'live' : 'lapsed';
			if (inRun) states.push(inRun.state);
			const row = `${leased ? '●' : '○'} ${cut(d.instance, 9).padEnd(9)} ${cut(what, 10)}`;
			texts.push({ x: x + 2, y: 10 + j, text: row, kind: inRun ? kindOf(inRun.state) : leased ? 'ok' : 'dim' });
		});
		if (states.some((s) => kindOf(s) === 'fail')) marks.push({ box: id, kind: 'fail' });
		else if (states.length && states.every((s) => kindOf(s) === 'ok')) marks.push({ box: id, kind: 'ok' });
		else if (states.some((s) => kindOf(s) === 'coord')) marks.push({ box: id, kind: 'coord' });
		if (live && (state === 'preparing' || state === 'cleanup')) lit.push({ edge: `n-${name}`, kind: state === 'cleanup' ? 'own' : 'notify' });
	});
	const width = Math.max(gx + w, trunk + 18) + 1;
	const height = Math.max(5 + 6 + tallest + 2, 7 + targets.length * 6) + 1;
	return { width, height, boxes, edges, steps: [{ name: group, text: '', lit, marks, texts }] };
}

/** The drawing in words, for whoever does not see it: each dispatcher with its instances, and
 * while a run goes, where its load goes. */
export function describe(group: string, dispatchers: Dispatcher[], live?: LiveRun): string {
	const names = [...new Set(dispatchers.map((d) => d.name))];
	const now = Date.now();
	const parts = names.map((n) => `${n} (${dispatchers.filter((d) => d.name === n).map((d) => (Date.parse(d.leaseExpiresAt) > now ? d.instance : `${d.instance} lapsed`)).join(', ')})`);
	let out = `${group}: ${parts.join(', ') || 'no dispatchers'}.`;
	if (live) {
		const r = live.run;
		const states = r.dispatchers.map((i) => `${i.name}/${i.instance} ${i.state}`);
		out += ` Its run ${r.id} is ${r.state}${states.length ? `: ${states.join(', ')}` : ''}.`;
		const loads = (live.progress?.load ?? []).filter((l) => l.target).map((l) => `${l.stream} to ${l.target}`);
		if (loads.length) out += ` The load goes ${loads.join(', ')}.`;
	}
	return out;
}

/** The scene's characters, ready for a <pre class="adg-grid">. */
export const draw = (s: Scene) => render(s)[0].html;
