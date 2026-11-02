/**
 * Lightning on the hero's words (AsciiAxe.astro). While a word shows, arcs crawl across its
 * letters in characters, a white-hot head and a fading tail, and some of its cells glint as charged
 * metal does; it builds word by word towards the axe's strike. When the axe strikes, the last word
 * flashes white and is electrocuted away, fast: from where the axe hit, outwards, each cell turns
 * to crackling lightning, burns white, and is gone.
 */

/** How many arcs crawl at once over each word. */
const ARCS: Record<string, number> = { OPS: 4, MEET: 6, STORM: 9, BREAKER: 12 };
/** Cells an arc crawls a second, and how long it hangs once it has. */
const SPEED = 170;
const HANG = .07;
const INK = '=+*#%@';
const CRACKLE = '-/\\|+*';

type Cell = { x: number; y: number; ch: string };
interface Arc { cells: Cell[]; born: number }
type Lit = { ch: string; kind: string };

const escape = (ch: string) => (ch === '<' ? '&lt;' : ch === '>' ? '&gt;' : ch === '&' ? '&amp;' : ch);
const pick = <T>(of: ArrayLike<T>): T => of[Math.floor(Math.random() * of.length)];
// A cell's own fixed chance, so a cell burns at the same moment in every frame.
const chance = (x: number, y: number) => {
	const v = Math.sin(x * 127.1 + y * 311.7) * 43758.5453;
	return v - Math.floor(v);
};

/** Lightning for a hero: crawl renders a word with its arcs; burn renders it being electrocuted. */
export function createSizzle() {
	let word = '';
	let rows: string[] = [];
	let width = 0;
	let ink: [number, number][] = [];
	let edges: [number, number][] = [];
	let box = { left: 0, right: 0, top: 0, bottom: 0 };
	let burnsAt = new Float32Array(0);
	let ignited = '';
	let arcs: Arc[] = [];

	const learn = (art: string, next: string) => {
		word = next;
		rows = art.split('\n');
		width = Math.max(...rows.map((r) => r.length));
		ink = [];
		box = { left: Infinity, right: -Infinity, top: Infinity, bottom: -Infinity };
		rows.forEach((row, y) => [...row].forEach((ch, x) => {
			if (!INK.includes(ch)) return;
			ink.push([x, y]);
			box = { left: Math.min(box.left, x), right: Math.max(box.right, x), top: Math.min(box.top, y), bottom: Math.max(box.bottom, y) };
		}));
		// The cells of a letter's edge: an arc that starts there runs out over the dark, where it
		// shows, as often as into the letter.
		const inked = (x: number, y: number) => INK.includes(rows[y]?.[x] ?? ' ');
		edges = ink.filter(([x, y]) => !inked(x - 1, y) || !inked(x + 1, y) || !inked(x, y - 1) || !inked(x, y + 1));
		arcs = [];
		ignited = '';
	};

	// When each cell burns, for a strike at a cell: sooner the nearer it, so the burn spreads out
	// from where the axe hit, and a little ragged, so it eats its way rather than sweeping. A cell
	// is half a row wide, so the spread is round on the screen.
	const ignite = (ox: number, oy: number) => {
		ignited = `${ox},${oy}`;
		burnsAt = new Float32Array(rows.length * width);
		let reach = 1;
		rows.forEach((row, y) => [...row].forEach((ch, x) => { if (ch !== ' ') reach = Math.max(reach, Math.hypot((x - ox) * .55, y - oy)); }));
		rows.forEach((row, y) => [...row].forEach((ch, x) => {
			if (ch === ' ') return;
			burnsAt[y * width + x] = .04 + .66 * Math.hypot((x - ox) * .55, y - oy) / reach + .14 * chance(x, y);
		}));
	};

	// A jagged run from a cell of a letter: always on sideways, often a row up or down.
	const walk = (x: number, y: number, dx: number, length: number, upright: boolean): Cell[] => {
		const cells: Cell[] = [];
		for (let i = 0; i < length; i++) {
			const r = Math.random();
			const dy = upright ? (r < .5 ? -1 : 1) : r < .28 ? -1 : r < .56 ? 1 : 0;
			const sx = upright && Math.random() < .5 ? 0 : dx;
			x += sx; y += dy;
			if (x < box.left - 2 || x > box.right + 2 || y < box.top - 1 || y > box.bottom + 1) break;
			const ch = sx === 0 ? '|' : dy === 0 ? '-' : (sx > 0) === (dy < 0) ? '/' : '\\';
			cells.push({ x, y, ch });
		}
		return cells;
	};

	const spawn = (now: number, from?: [number, number][]) => {
		const [x, y] = pick(from?.length ? from : edges.length && Math.random() < .8 ? edges : ink);
		const cells = walk(x, y, Math.random() < .5 ? -1 : 1, 14 + Math.floor(Math.random() * 24), false);
		arcs.push({ cells, born: now });
		// Now and then a fork drops from it.
		if (cells.length > 4 && Math.random() < .7) {
			const from = pick(cells);
			arcs.push({ cells: walk(from.x, from.y, Math.random() < .5 ? -1 : 1, 4 + Math.floor(Math.random() * 7), true), born: now + .03 });
		}
	};

	// The arcs crawling now, as many as count, each cell lit as its place on its arc says.
	const crackle = (now: number, count: number, lit: Map<number, Lit>, from?: [number, number][]) => {
		arcs = arcs.filter((a) => now - a.born < a.cells.length / SPEED + HANG && now >= a.born - .5);
		while (arcs.filter((a) => a.born <= now).length < count) spawn(now, from);
		for (const a of arcs) {
			const shown = Math.min(a.cells.length, Math.floor((now - a.born) * SPEED) + 1);
			for (let i = 0; i < shown; i++) {
				const c = a.cells[i];
				const kind = i === shown - 1 && shown < a.cells.length ? 'sz-hot' : shown - i > 9 ? 'sz-tail' : 'sz';
				lit.set(c.y * width + c.x, { ch: c.ch, kind });
			}
		}
	};

	const render = (lit: Map<number, Lit>) => {
		let html = '';
		rows.forEach((row, y) => {
			if (y) html += '\n';
			const line = row.padEnd(width);
			let run = '';
			for (let x = 0; x < line.length; x++) {
				const l = lit.get(y * width + x);
				if (!l) { run += escape(line[x]); continue; }
				if (!l.kind) { run += escape(l.ch); continue; }
				html += run; run = '';
				html += `<span class="${l.kind}">${escape(l.ch)}</span>`;
			}
			html += run.replace(/\s+$/, '');
		});
		return html;
	};

	return {
		/** A word as it shows, lightning crawling over it, at a moment. */
		crawl(art: string, next: string, now: number): string {
			if (next !== word || rows.length === 0) learn(art, next);
			if (ink.length === 0) return art.split('').map(escape).join('');
			const lit = new Map<number, Lit>();
			crackle(now, ARCS[word] ?? 3, lit);
			for (let n = Math.ceil(ink.length * .012); n > 0; n--) {
				const [x, y] = pick(ink);
				const k = y * width + x;
				if (!lit.has(k)) lit.set(k, { ch: rows[y][x], kind: 'sz-glint' });
			}
			return render(lit);
		},

		/** A word being electrocuted away from the cell the axe struck, at progress (0 the strike,
		 * 1 gone). The strike's flash is the caller's, one style for the whole word. */
		burn(art: string, next: string, now: number, progress: number, strike: [number, number]): string {
			if (next !== word || rows.length === 0) learn(art, next);
			const [sx, sy] = strike.map(Math.round);
			if (ignited !== `${sx},${sy}`) ignite(sx, sy);
			const lit = new Map<number, Lit>();
			const front: [number, number][] = [];
			rows.forEach((row, y) => [...row].forEach((ch, x) => {
				if (ch === ' ') return;
				const k = y * width + x;
				const left = burnsAt[k] - progress;
				if (left > .12) return; // not reached yet
				// Crackling: only the white-hot edge glows, so a burn of thousands of cells paints fast.
				if (left > 0) {
					lit.set(k, { ch: pick(CRACKLE), kind: left < .04 ? 'sz-hot' : 'sz-burn' });
					if (left < .04) front.push([x, y]);
				}
				else if (left > -.06 && Math.random() < .35) lit.set(k, { ch: Math.random() < .5 ? '.' : ':', kind: 'sz-tail' }); // its ember
				else lit.set(k, { ch: ' ', kind: '' }); // gone
			}));
			// Lightning leaps from the burning edge, and dies away with it.
			if (ink.length) crackle(now, Math.round((ARCS[word] ?? 6) * 1.6 * (1 - progress)), lit, front);
			return render(lit);
		},
	};
}
