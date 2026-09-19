// The editor palette as data. The screen draws these values and the
// contrast gate measures this same text, so the two cannot drift apart.
// One dark block only. The page offers no theme switch, so a second block
// would measure a theme nobody can reach.
export const EDITOR_THEME_CSS = [
	':root {',
	'color-scheme: dark;',
	'--paper: #191410;',
	'--raised: #241d15;',
	'--ink: #f4edde;',
	'--muted: #d9cfbb;',
	'--accent: #e8a33d;',
	'--on-accent: #201809;',
	'--line: #5a4f41;',
	'}'
].join('\n');

// Every foreground and background pair the screen draws as text.
export const CONTRAST_PAIRS: ReadonlyArray<readonly [string, string]> = [
	['ink', 'paper'],
	['muted', 'paper'],
	['ink', 'raised'],
	['muted', 'raised'],
	['accent', 'paper'],
	['on-accent', 'accent']
];
