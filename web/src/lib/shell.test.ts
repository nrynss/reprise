import { describe, expect, it } from 'vitest';
import { appName, pageTitle } from './shell';

describe('pageTitle', () => {
	it('leads with the app name', () => {
		expect(pageTitle('Episodes')).toBe('Episodes · Reprise');
	});

	it('is the app name alone with no section', () => {
		expect(pageTitle()).toBe(appName);
	});
});
