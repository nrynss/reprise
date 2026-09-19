// Playwright run for the share and privacy screens. It points at
// the routes folder, so the spec beside the share route runs without
// touching the shared tests folder.
// npx playwright test -c "src/routes/share/share.playwright.config.ts"
import { defineConfig } from '@playwright/test';

export default defineConfig({
	testDir: '.',
	testMatch: ['share.spec.ts'],
	timeout: 60_000,
	webServer: {
		command: 'npm run preview -- --port 4176 --strictPort',
		port: 4176,
		reuseExistingServer: false
	},
	use: {
		baseURL: 'http://localhost:4176'
	},
	reporter: [['list']]
});
