/** How the axe's scene (axe-scene.ts) becomes characters: one a cell of a sample of it. */
import { MATERIAL_OFFSET } from './axe-scene';

export type Theme = 'dark' | 'light';
type RGB = [number, number, number];
type Ramp = [RGB, RGB];
// The colors of the axe's materials, from a sparse character's to a dense one's: on the dark theme
// they brighten out of the night, on the light one they darken out of the overcast. The haft is
// gunmetal, and its grip's cord and the head's spine carry the storm's charge.
const MATERIALS: Record<Theme, { wood: Ramp; wrap: Ramp; paint: Ramp }> = {
	dark: { wood: [[22, 26, 36], [118, 132, 156]], wrap: [[14, 40, 74], [98, 190, 255]], paint: [[16, 52, 92], [150, 220, 255]] },
	light: { wood: [[120, 130, 150], [36, 44, 62]], wrap: [[96, 136, 206], [14, 62, 168]], paint: [[90, 140, 214], [10, 56, 160]] },
};
// Lightning, from its halo to its core.
const BOLT: Record<Theme, [number, RGB][]> = {
	dark: [[0, [44, 92, 170]], [.5, [100, 196, 255]], [1, [236, 250, 255]]],
	light: [[0, [112, 152, 222]], [.6, [28, 90, 212]], [1, [8, 40, 140]]],
};
const GLYPHS = '.,:;=+*#%@';

const rgb = (c: RGB) => `rgb(${c.map(Math.round).join(', ')})`;
function along(ramp: [number, RGB][], f: number): RGB {
	const t = Math.max(0, Math.min(1, f));
	const b = ramp.findIndex(([at]) => at >= t);
	if (b <= 0) return ramp[0][1];
	const [a0, from] = ramp[b - 1], [a1, to] = ramp[b], p = (t - a0) / (a1 - a0);
	return from.map((c, i) => c + (to[i] - c) * p) as RGB;
}

/** The character and color of a sample's pixel at index (RGBA, not premultiplied), or none. */
export function asciiCell(pixels: ArrayLike<number>, index: number, theme: Theme): { glyph: string; color: string } | undefined {
	const value = pixels[index] * pixels[index + 3] / 255;
	if (value < 9) return;
	const isLine = pixels[index] - pixels[index + 1] > 12;
	// Blue against red is the material: wood above, the grip's wrap twice as far above,
	// paint below, steel and streaks even.
	const tint = isLine ? 0 : pixels[index + 2] - pixels[index];
	const material = tint > MATERIAL_OFFSET * 1.5 ? 'wrap' : tint > MATERIAL_OFFSET / 2 ? 'wood' : tint < -MATERIAL_OFFSET / 2 ? 'paint' : undefined;
	let color: string;
	if (material) {
		const [from, to] = MATERIALS[theme][material], f = Math.min(1, value / 230);
		color = rgb(from.map((c, i) => c + (to[i] - c) * f) as RGB);
	} else if (theme === 'light') {
		// Storm-blue ink on the overcast: sparse characters mid slate, dense ones near navy.
		const ink = 150 - value * .46;
		color = isLine ? rgb([ink * .92, ink, ink * 1.12]) : rgb([ink * .8, ink * .88, ink + 14]);
	} else if (isLine) {
		// The wind's streaks, as rain.
		const tone = 30 + value * .6;
		color = rgb([tone * .76, tone * .94, Math.min(255, tone * 1.26)]);
	} else {
		// Steel, cold and blue, bright where the light catches it.
		const tone = 24 + value * .64;
		color = rgb([tone * .9, tone * .97, Math.min(255, tone * 1.1 + 6)]);
	}
	const direction = (pixels[index + 2] - pixels[index + 1]) / Math.max(1, pixels[index] - pixels[index + 1]);
	const lineGlyph = direction < .15 ? '-' : direction < .45 ? '/' : direction < .75 ? '\\' : '|';
	return { glyph: isLine ? lineGlyph : GLYPHS[Math.min(GLYPHS.length - 1, Math.floor(value / 256 * GLYPHS.length))], color };
}

/**
 * The character of the lightning layer's pixel at index, or none. A bolt's core draws over the
 * axe; its halo only where the axe leaves the cell empty.
 */
export function stormCell(pixels: ArrayLike<number>, index: number, theme: Theme): { glyph: string; color: string; over: boolean } | undefined {
	const charge = pixels[index + 2] * pixels[index + 3] / 255;
	if (charge < 30) return;
	if (charge >= 150) {
		const g = pixels[index + 1];
		return { glyph: g < 60 ? '-' : g < 120 ? '/' : g < 180 ? '\\' : '|', color: rgb(along(BOLT[theme], .55 + charge / 255 * .45)), over: true };
	}
	return { glyph: charge > 52 ? ':' : '.', color: rgb(along(BOLT[theme], charge / 150 * .5)), over: false };
}
