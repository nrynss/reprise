// Playwright run for the mock voice take. It points at this folder, so the
// specs beside the route run without touching the shared tests folder.
// The match keeps unit files out. They belong to the unit runner.
// npx playwright test -c src/routes/record/mock.playwright.config.ts
import { defineConfig } from '@playwright/test';

export default defineConfig({
	testDir: '.',
	testMatch: ['*.spec.ts'],
	timeout: 60_000,
	webServer: {
		command: 'npm run preview -- --port 4174 --strictPort',
		port: 4174,
		reuseExistingServer: false
	},
	use: {
		baseURL: 'http://localhost:4174'
	},
	reporter: [['list']]
});
