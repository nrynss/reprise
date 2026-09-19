// Playwright run for the editor screen. It points at this folder, so the
// spec beside the route runs without touching the shared tests folder.
// npx playwright test -c "src/routes/episode/[id]/edit/edit.playwright.config.ts"
import { defineConfig } from '@playwright/test';

export default defineConfig({
	testDir: '.',
	timeout: 60_000,
	webServer: {
		command: 'npm run preview -- --port 4175 --strictPort',
		port: 4175,
		reuseExistingServer: false
	},
	use: {
		baseURL: 'http://localhost:4175'
	},
	reporter: [['list']]
});
