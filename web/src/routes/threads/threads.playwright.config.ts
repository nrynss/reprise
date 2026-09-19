// Playwright run for the gallery, the episode view, and the thread
// panel. It points at the routes folder, so the spec beside the
// threads route runs without touching the shared tests folder.
// npx playwright test -c "src/routes/threads/threads.playwright.config.ts"
import { defineConfig } from '@playwright/test';

export default defineConfig({
	testDir: '.',
	testMatch: ['threads.spec.ts'],
	timeout: 60_000,
	webServer: {
		command: 'npm run preview -- --port 4177 --strictPort',
		port: 4177,
		reuseExistingServer: false
	},
	use: {
		baseURL: 'http://localhost:4177'
	},
	reporter: [['list']]
});
