// Playwright run for the shared layout proofs. It matches only the
// spec beside these components, so it runs without touching the
// shared tests folder or starting a preview server. The spec renders
// its sample through setContent, so no route takes part.
// npx playwright test -c src/lib/components/layout.playwright.config.ts
import { defineConfig } from '@playwright/test';

export default defineConfig({
	testDir: '.',
	testMatch: ['layout.spec.ts'],
	timeout: 30_000,
	projects: [
		{ name: 'chromium', use: { browserName: 'chromium' } },
		{ name: 'firefox', use: { browserName: 'firefox' } }
	],
	reporter: [['list']]
});
