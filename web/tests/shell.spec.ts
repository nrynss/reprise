import { expect, test } from '@playwright/test';

test('the shell renders the app name', async ({ page }) => {
	await page.goto('/');
	await expect(page.getByRole('heading', { level: 1 })).toHaveText('The season so far');
});
