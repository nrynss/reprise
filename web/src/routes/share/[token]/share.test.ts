// Checks the share client against fixed bodies. Every case decodes
// a literal, so no clock and no network stand anywhere near these
// tests.
import { describe, expect, it } from 'vitest';
import {
	absoluteCoverUrl,
	DEAD_HEADING,
	fetchShare,
	fixtureShare,
	formatEpisodeNumber,
	parseShare,
	shareAudioUrl,
	shareByline,
	SHARE_DESC,
	SHARE_FOOTER,
	SHARE_MISSING,
	START_OWN,
	type FetchFn
} from './share';

const PUBLISHED = JSON.stringify({
	episode_id: 'ep-1',
	title: 'Three weeks of almost',
	number: 4,
	author: 'Mara',
	audio_media_id: 'blob-opus',
	cover_path: '/api/share/token-1/cover'
});

const REFUSED = JSON.stringify({
	error: { code: SHARE_MISSING, message: 'that episode opens nothing' }
});

function fetchOk(body: string): FetchFn {
	return async () => new Response(body, { status: 200 });
}

function fetchRefused(): FetchFn {
	return async () => new Response(REFUSED, { status: 404 });
}

describe('parseShare', () => {
	it('decodes the render and cover behind a published token', () => {
		const payload = parseShare(PUBLISHED);
		expect(payload.title).toBe('Three weeks of almost');
		expect(payload.author).toBe('Mara');
		expect(payload.audio_media_id).toBe('blob-opus');
		expect(payload.cover_path).toBe('/api/share/token-1/cover');
	});

	it('throws the envelope code for a revoked link', () => {
		expect(() => parseShare(REFUSED)).toThrow(SHARE_MISSING);
	});

	it('throws the missing code for a body with no author in it', () => {
		expect(() => parseShare('{"title":"x"}')).toThrow(SHARE_MISSING);
		expect(() => parseShare('not json')).toThrow(SHARE_MISSING);
	});
});

describe('fetchShare', () => {
	it('resolves a published token to its payload', async () => {
		const payload = await fetchShare(fetchOk(PUBLISHED), 'token-1');
		expect(payload.episode_id).toBe('ep-1');
	});

	it('throws the missing code for a revoked link', async () => {
		await expect(fetchShare(fetchRefused(), 'token-1')).rejects.toThrow(SHARE_MISSING);
	});
});

describe('presentation', () => {
	it('streams the render from the media store', () => {
		const payload = parseShare(PUBLISHED);
		expect(shareAudioUrl(payload)).toBe('/media/blob-opus');
	});

	it('numbers episodes with two digits', () => {
		expect(formatEpisodeNumber(4)).toBe('EP.04');
	});

	it('fixtures carry a playable published link', () => {
		const payload = fixtureShare();
		expect(payload.title).not.toBe('');
		expect(payload.author).toBe('Mara');
		expect(shareAudioUrl(payload)).toContain('/media/');
	});

	it('fixtures carry no author when the owner set no name', () => {
		expect(fixtureShare('').author).toBe('');
		expect(shareByline('')).toBe('');
	});
});

describe('byline and preview', () => {
	it('names the author beside the title when set', () => {
		expect(shareByline('Mara')).toBe('by Mara');
	});

	it('keeps the footer to one line and one link name', () => {
		expect(SHARE_FOOTER).toBe(
			'Made with Reprise, a podcast of your own life, hosted by someone who remembers.'
		);
		expect(START_OWN).toBe('Start your own');
		expect(DEAD_HEADING).toBe('This episode is no longer shared.');
		expect(SHARE_DESC).toBe('A shared episode from Reprise.');
	});

	it('builds the absolute cover address for preview tags', () => {
		expect(absoluteCoverUrl('https://example.test', '/api/share/t/cover')).toBe(
			'https://example.test/api/share/t/cover'
		);
		expect(absoluteCoverUrl('', '/api/share/t/cover')).toBe('');
		expect(absoluteCoverUrl('https://example.test', '')).toBe('');
	});
});
