// Playwright run for the gallery screen. It matches only the spec beside
// this page, so it runs without touching the shared tests folder.
// npx playwright test -c src/routes/gallery.playwright.config.ts
import { defineConfig } from '@playwright/test';

export default defineConfig({
	testDir: '.',
	testMatch: 'gallery.spec.ts',
	timeout: 30_000,
	webServer: {
		command: 'npm run preview -- --port 4176 --strictPort',
		port: 4176,
		reuseExistingServer: false
	},
	use: {
		baseURL: 'http://localhost:4176'
	},
	projects: [
		{ name: 'chromium', use: { browserName: 'chromium' } },
		{ name: 'webkit', use: { browserName: 'webkit' } }
	],
	reporter: [['list']]
});
