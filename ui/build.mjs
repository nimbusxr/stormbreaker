// Builds the UI's web app into internal/ui/dist, which the stormbreaker binary embeds
// (go generate ./internal/ui runs it). The files there are generated: edit src/ instead.
import { build } from 'esbuild';
import { copyFileSync, mkdirSync, rmSync } from 'node:fs';

const out = new URL('../internal/ui/dist/', import.meta.url).pathname;
rmSync(out, { recursive: true, force: true });
mkdirSync(`${out}assets`, { recursive: true });
await build({
	entryPoints: ['src/main.ts'],
	bundle: true,
	minify: true,
	format: 'esm',
	target: 'es2022',
	outfile: `${out}assets/app.js`,
	legalComments: 'none',
	logLevel: 'warning',
});
copyFileSync('src/index.html', `${out}index.html`);
copyFileSync('src/app.css', `${out}assets/app.css`);
// The docs' icon and typeface, and its licence.
copyFileSync('../docs/public/favicon.svg', `${out}assets/favicon.svg`);
copyFileSync('../docs/public/fonts/inter-latin.woff', `${out}assets/inter-latin.woff`);
copyFileSync('../docs/public/fonts/OFL.txt', `${out}assets/inter-OFL.txt`);
console.log('ui: built into internal/ui/dist');
