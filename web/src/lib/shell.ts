// Placeholders the shell renders before real screens exist.
// Real screens replace them in their own change.
export const appName = 'Reprise';
export const appTagline = 'A theme that returns later.';

// pageTitle composes the document title. The app name always leads so a
// crowded tab list still names the product first.
export function pageTitle(section?: string): string {
	if (!section) return appName;
	return `${section} · ${appName}`;
}
