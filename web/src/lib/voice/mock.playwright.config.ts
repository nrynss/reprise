// Playwright run for the controller draft move and the voice guards. It points
// at this folder, so both specs beside the controller run without touching
// the shared tests folder.
// npx playwright test -c src/lib/voice/mock.playwright.config.ts
import { defineConfig } from '@playwright/test';

export default defineConfig({
	testDir: '.',
	testMatch: ['controller-complete.spec.ts', 'session-guard.spec.ts'],
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
