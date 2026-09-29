// Playwright run for the sign-in screens. It points at this folder, so
// the spec beside the route runs without touching the shared tests folder.
// The match keeps unit files out. They belong to the unit runner. WebKit
// cannot launch on this host, so Chromium and Firefox carry the proofs.
// npx playwright test -c src/routes/account/account.playwright.config.ts
import { defineConfig } from '@playwright/test';

export default defineConfig({
	testDir: '.',
	testMatch: ['account.spec.ts'],
	timeout: 60_000,
	webServer: {
		command: 'npm run preview -- --port 4179 --strictPort',
		port: 4179,
		reuseExistingServer: false
	},
	use: {
		baseURL: 'http://localhost:4179'
	},
	projects: [
		{ name: 'chromium', use: { browserName: 'chromium' } },
		{ name: 'firefox', use: { browserName: 'firefox' } }
	],
	reporter: [['list']]
});
