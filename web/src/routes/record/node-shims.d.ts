// Minimal shapes for the node builtins the mock run uses. This file has
// no imports or exports, so each block is an ambient declaration and not
// an augmentation. The web typecheck then resolves the matching imports
// in the spec beside it without node types on disk.
declare module 'child_process' {
	export function execFileSync(file: string, args: string[], options: { encoding: string }): string;
}

declare module 'fs' {
	export function mkdirSync(path: string, options: { recursive: boolean }): void;
	export function writeFileSync(path: string, data: Uint8Array): void;
}

declare module 'os' {
	export function tmpdir(): string;
}

declare module 'path' {
	export function join(...parts: string[]): string;
}
