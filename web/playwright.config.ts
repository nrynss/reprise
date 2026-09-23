import { defineConfig } from '@playwright/test';

export default defineConfig({
	testDir: 'tests',
	timeout: 30_000,
	webServer: {
		command: 'npm run preview -- --port 4173 --strictPort',
		port: 4173,
		reuseExistingServer: false
	},
	use: {
		baseURL: 'http://localhost:4173'
	},
	projects: [
		{ name: 'chromium', use: { browserName: 'chromium' } },
		{ name: 'webkit', use: { browserName: 'webkit' } }
	],
	reporter: [['list']]
});
