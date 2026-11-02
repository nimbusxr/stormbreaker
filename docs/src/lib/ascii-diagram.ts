/**
 * The diagrams' engine: a scene laid out on a grid of characters, drawn with box-drawing
 * characters the way the home page's axe is drawn with ASCII, and told in steps a reader clicks
 * through (src/components/diagrams/AsciiDiagram.astro). Everything here runs at build time; the
 * page gets each step's grid as text, and the paths its packets ride.
 */

/** What a character is, which is its color (diagrams.css). */
export type Kind =
	| 'line' | 'zone' | 'key' | 'title' | 'sub' | 'label' | 'dim'
	| 'coord' | 'notify' | 'traffic' | 'own' | 'fail' | 'ok';
export type Point = [x: number, y: number];

interface Shown {
	/** The step it appears at (0, the first, by default). */
	from?: number;
	/** The step from which it is drawn dim, once something has superseded it. */
	dimFrom?: number;
	/** The step after which it is gone. */
	until?: number;
}
export interface Box extends Shown {
	id: string;
	x: number;
	y: number;
	w: number;
	h: number;
	title: string;
	lines?: string[];
	/** light: a component; heavy: a key one, double-ruled; zone: where things run, dashed. */
	style?: 'light' | 'heavy' | 'zone';
	kind?: Kind;
	/** The title inside the box, on its first row, rather than on its top rule: a state. */
	inside?: boolean;
}
export interface Edge extends Shown {
	id: string;
	/** Corners of an orthogonal path, in cells; it is drawn through every cell between them. */
	path: Point[];
	arrow?: boolean;
	/** An arrow at its start too: a line both ways. */
	twoWay?: boolean;
	/** The way a one-cell edge points. */
	dir?: 'up' | 'down' | 'left' | 'right';
}
export interface Text extends Shown {
	x: number;
	y: number;
	text: string;
	kind?: Kind;
}
export interface Lit {
	edge: string;
	kind: Kind;
	/** Packets riding the edge while the step shows. */
	packets?: number;
	reverse?: boolean;
	/** Cells a second. */
	speed?: number;
}
export interface Step {
	name: string;
	text: string;
	lit?: Lit[];
	marks?: { box: string; kind: Kind }[];
	texts?: Text[];
}
export interface Scene {
	width: number;
	height: number;
	boxes?: Box[];
	edges?: Edge[];
	texts?: Text[];
	steps: Step[];
}

export interface Packets {
	path: Point[];
	kind: Kind;
	count: number;
	speed: number;
}
export interface RenderedStep {
	name: string;
	text: string;
	html: string;
	packets: Packets[];
}

// A cell of an edge also knows the sides its lines leave by (north 1, east 2, south 4, west 8), so
// that where edges meet, branch or cross, the character joins them all. A cell of a lit edge knows
// how far along the edge it is (pulse), so that a brighter band can run the way the edge goes.
type Cell = { ch: string; k: Kind | ''; conn?: number; pulse?: number };
type Grid = Cell[][];

const RULES = {
	light: { tl: '┌', tr: '┐', bl: '└', br: '┘', h: '─', v: '│' },
	heavy: { tl: '╔', tr: '╗', bl: '╚', br: '╝', h: '═', v: '║' },
	zone: { tl: '┌', tr: '┐', bl: '└', br: '┘', h: '╌', v: '╎' },
};

const shown = (e: Shown, step: number) => (e.from ?? 0) <= step && (e.until === undefined || step <= e.until);
const dimmed = (e: Shown, step: number) => e.dimFrom !== undefined && e.dimFrom <= step;

function put(grid: Grid, x: number, y: number, ch: string, k: Kind | '') {
	if (y < 0 || y >= grid.length || x < 0 || x >= grid[0].length) return;
	grid[y][x] = { ch, k };
}

function write(grid: Grid, x: number, y: number, text: string, k: Kind) {
	[...text].forEach((ch, i) => put(grid, x + i, y, ch, ch === ' ' && !grid[y]?.[x + i]?.ch.trim() ? '' : k));
}

function drawBox(grid: Grid, b: Box, kind: Kind) {
	const r = RULES[b.style ?? 'light'];
	const right = b.x + b.w - 1, bottom = b.y + b.h - 1;
	for (let x = b.x; x <= right; x++) {
		put(grid, x, b.y, r.h, kind);
		put(grid, x, bottom, r.h, kind);
	}
	for (let y = b.y; y <= bottom; y++) {
		put(grid, b.x, y, r.v, kind);
		put(grid, right, y, r.v, kind);
	}
	put(grid, b.x, b.y, r.tl, kind); put(grid, right, b.y, r.tr, kind);
	put(grid, b.x, bottom, r.bl, kind); put(grid, right, bottom, r.br, kind);
	// The title sits on the top rule, as a terminal frame's does, or inside, as a state's.
	if (b.inside) write(grid, b.x + 2, b.y + 1, b.title, 'title');
	else if (b.title) write(grid, b.x + 2, b.y, ` ${b.title} `, b.style === 'zone' ? 'label' : 'title');
	b.lines?.forEach((line, i) => write(grid, b.x + 2, b.y + 1 + (b.inside ? 1 : 0) + i, line, 'sub'));
}

/** Every cell of a path, each with the direction it is entered and left by. */
const DIRS = { up: [0, -1], down: [0, 1], left: [-1, 0], right: [1, 0] } as const;

function cells(path: Point[], dir?: keyof typeof DIRS): { x: number; y: number; dx: number; dy: number; inDx: number; inDy: number }[] {
	if (path.length === 1) {
		const [dx, dy] = DIRS[dir ?? 'down'];
		return [{ x: path[0][0], y: path[0][1], dx, dy, inDx: dx, inDy: dy }];
	}
	const out: { x: number; y: number; dx: number; dy: number; inDx: number; inDy: number }[] = [];
	let inDx = 0, inDy = 0;
	for (let i = 0; i < path.length - 1; i++) {
		const [x0, y0] = path[i], [x1, y1] = path[i + 1];
		const dx = Math.sign(x1 - x0), dy = Math.sign(y1 - y0);
		if (dx && dy) throw new Error(`a diagram's edge must turn at right angles: ${path[i]} to ${path[i + 1]}`);
		let x = x0, y = y0;
		// A segment's first cell is the corner it turns at: entered the way the last one went.
		if (i === 0) { inDx = dx; inDy = dy; }
		while (x !== x1 || y !== y1) {
			out.push({ x, y, dx, dy, inDx, inDy });
			inDx = dx; inDy = dy;
			x += dx; y += dy;
		}
	}
	const [lx, ly] = path[path.length - 1];
	const last = out[out.length - 1];
	out.push({ x: lx, y: ly, dx: last?.dx ?? 0, dy: last?.dy ?? 0, inDx: last?.dx ?? 0, inDy: last?.dy ?? 0 });
	return out;
}

const N = 1, E = 2, S = 4, W = 8;
const side = (dx: number, dy: number) => (dx > 0 ? E : dx < 0 ? W : dy > 0 ? S : N);
const JOIN: Record<number, string> = {
	[N]: '│', [S]: '│', [N | S]: '│', [E]: '─', [W]: '─', [E | W]: '─',
	[E | S]: '┌', [S | W]: '┐', [N | E]: '└', [N | W]: '┘',
	[N | E | S]: '├', [N | S | W]: '┤', [E | S | W]: '┬', [N | E | W]: '┴', [N | E | S | W]: '┼',
};

const TEE: Record<string, string> = {
	'─s': '┬', '─n': '┴', '│e': '├', '│w': '┤', '═s': '╤', '═n': '╧', '║e': '╟', '║w': '╢', '╌s': '┬', '╌n': '┴', '╎e': '├', '╎w': '┤',
};

function drawEdge(grid: Grid, e: Edge, kind: Kind, lit?: Lit) {
	const path = cells(e.path, e.dir);
	// A lit edge pulses from where it starts to its arrow, or back, for one lit in reverse.
	const pulse = (i: number) => {
		const c = grid[path[i].y]?.[path[i].x];
		if (lit && c) c.pulse = lit.reverse ? path.length - 1 - i : i;
	};
	path.forEach((c, i) => {
		const last = i === path.length - 1;
		if (last && e.arrow !== false) {
			put(grid, c.x, c.y, c.dx > 0 ? '>' : c.dx < 0 ? '<' : c.dy > 0 ? 'v' : '^', kind);
			pulse(i);
			return;
		}
		if (i === 0 && e.twoWay && path.length > 1) {
			put(grid, c.x, c.y, c.dx > 0 ? '<' : c.dx < 0 ? '>' : c.dy > 0 ? '^' : 'v', kind);
			pulse(i);
			return;
		}
		// The sides this cell's line leaves by: back the way it came, and on the way it goes.
		let conn = side(-c.inDx, -c.inDy) | (last ? 0 : side(c.dx, c.dy));
		if (last) conn |= side(c.dx, c.dy);
		const under = grid[c.y]?.[c.x];
		if (under?.conn) conn |= under.conn;
		put(grid, c.x, c.y, JOIN[conn] ?? '┼', kind);
		grid[c.y][c.x].conn = conn;
		pulse(i);
	});
	// Where it leaves a box, the box's rule takes a tee; a line both ways has an arrow there.
	if (e.twoWay) return;
	const first = path[0];
	const bx = first.x - first.dx, by = first.y - first.dy;
	const rule = grid[by]?.[bx];
	const dir = first.dx > 0 ? 'e' : first.dx < 0 ? 'w' : first.dy > 0 ? 's' : 'n';
	if (rule && TEE[rule.ch + dir]) put(grid, bx, by, TEE[rule.ch + dir], rule.k);
}

const escape = (s: string) => s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');

function html(grid: Grid): string {
	return grid.map((row) => {
		let out = '', run = '', k: Kind | '' = '';
		const flush = () => { if (run) out += k ? `<span class="k-${k}">${escape(run)}</span>` : escape(run); run = ''; };
		for (const c of row) {
			// A cell of a lit edge is a span of its own, that knows how far along the edge it is.
			if (c.pulse !== undefined && c.k) {
				flush();
				k = '';
				out += `<span class="k-${c.k} adg-pulse" style="--p:${c.pulse}">${escape(c.ch)}</span>`;
				continue;
			}
			if (c.k !== k) { flush(); k = c.k; }
			run += c.ch;
		}
		flush();
		return out.replace(/\s+$/, '');
	}).join('\n');
}

/** The scene drawn at each of its steps. */
export function render(scene: Scene): RenderedStep[] {
	const byId = new Map((scene.edges ?? []).map((e) => [e.id, e]));
	return scene.steps.map((step, i) => {
		const grid: Grid = Array.from({ length: scene.height }, () => Array.from({ length: scene.width }, () => ({ ch: ' ', k: '' as const })));
		const lit = new Map<string, Lit>();
		for (const l of step.lit ?? []) if (!lit.has(l.edge)) lit.set(l.edge, l);
		const marked = new Map((step.marks ?? []).map((m) => [m.box, m.kind]));
		for (const b of scene.boxes ?? []) {
			if (!shown(b, i)) continue;
			drawBox(grid, b, dimmed(b, i) ? 'dim' : marked.get(b.id) ?? b.kind ?? (b.style === 'heavy' ? 'key' : b.style === 'zone' ? 'zone' : 'line'));
		}
		// Unlit edges first, so the lit ones are drawn over them where they meet.
		for (const e of scene.edges ?? []) if (shown(e, i) && !lit.has(e.id)) drawEdge(grid, e, dimmed(e, i) ? 'dim' : 'line');
		for (const e of scene.edges ?? []) if (shown(e, i) && lit.has(e.id)) drawEdge(grid, e, lit.get(e.id)!.kind, lit.get(e.id));
		for (const t of [...(scene.texts ?? []), ...(step.texts ?? [])]) {
			if (shown(t, i)) write(grid, t.x, t.y, t.text, dimmed(t, i) ? 'dim' : t.kind ?? 'label');
		}
		const packets = (step.lit ?? []).filter((l) => l.packets).map((l) => {
			const e = byId.get(l.edge);
			if (!e) throw new Error(`a step lights ${l.edge}, which the scene has not`);
			const path = cells(e.path, e.dir).map(({ x, y }) => [x, y] as Point);
			return { path: l.reverse ? path.reverse() : path, kind: l.kind, count: l.packets!, speed: l.speed ?? 14 };
		});
		return { name: step.name, text: step.text, html: html(grid), packets };
	});
}
