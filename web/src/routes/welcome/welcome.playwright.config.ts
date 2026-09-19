// Playwright run for the welcome screen. It points at this folder, so
// the spec beside the route runs without touching the shared tests folder.
// npx playwright test -c "src/routes/welcome/welcome.playwright.config.ts"
import { defineConfig } from '@playwright/test';

export default defineConfig({
	testDir: '.',
	testMatch: ['welcome.spec.ts'],
	timeout: 60_000,
	webServer: {
		command: 'npm run preview -- --port 4178 --strictPort',
		port: 4178,
		reuseExistingServer: false
	},
	projects: [{ name: 'chromium', use: { browserName: 'chromium' } }],
	reporter: [['list']]
});
