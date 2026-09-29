// Minimal shapes for the file helpers the layout proofs use. The web
// package ships no node types, and the spec only reads one file.
declare module 'node:fs' {
	export function readFileSync(path: string, encoding: string): string;
}
declare module 'node:path' {
	export function dirname(path: string): string;
	export function join(...parts: string[]): string;
}
declare module 'node:url' {
	export function fileURLToPath(url: string): string;
}
