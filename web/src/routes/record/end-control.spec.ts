// Playwright pins for the standing end control on the record page. At mount
// the snapshot is preflight, so the live section is not rendered yet and a
// one time lookup cannot reach the live button. These proofs drive the mock
// take, press the live rendered End session button by mouse and by keyboard,
// and expect the armed Confirm end session state. This spec runs with the
// mock suite beside the route:
// npx playwright test -c src/routes/record/mock.playwright.config.ts end-control.spec.ts

import { expect, test } from '@playwright/test';

async function startMockTake(page: import('@playwright/test').Page): Promise<void> {
	await page.goto('/record?mock=1');
	await page.getByRole('button', { name: 'Start session' }).focus();
	await page.keyboard.press('Enter');
	await expect(page.getByRole('heading', { name: 'On air' })).toBeVisible();
}

test('the standing end control arms on mouse click', async ({ page }) => {
	await startMockTake(page);
	const endButton = page.getByRole('button', { name: 'End session', exact: true });
	await expect(endButton).toBeEnabled();
	await endButton.click();
	await expect(page.getByRole('button', { name: 'Confirm end session', exact: true })).toBeVisible();
	await expect(page.getByRole('status')).toContainText('Press end again');
});

test('the standing end control arms on keyboard Enter', async ({ page }) => {
	await startMockTake(page);
	const endButton = page.getByRole('button', { name: 'End session', exact: true });
	await expect(endButton).toBeEnabled();
	await endButton.focus();
	await expect(endButton).toBeFocused();
	await page.keyboard.press('Enter');
	await expect(page.getByRole('button', { name: 'Confirm end session', exact: true })).toBeVisible();
	await expect(page.getByRole('status')).toContainText('Press end again');
});
