// Pins for the processing steps. The draft stays waiting until the upload
// and both passes have finished, including when a pass has no job yet.

import { describe, expect, it } from 'vitest';
import {
	draftStep,
	readingFromJob,
	stoppedDetail,
	uploadDetail,
	uploadPercent,
	type ProcessingStep
} from './processing-state';

const waiting: ProcessingStep = { name: 'Transcription', detail: 'Waiting.', state: 'waiting', percent: 0 };

describe('draftStep', () => {
	it('stays waiting when either pass has not finished', () => {
		expect(draftStep('done', 'waiting', 'waiting').state).toBe('waiting');
		expect(draftStep('done', 'done', 'waiting').state).toBe('waiting');
		expect(draftStep('running', 'done', 'done').state).toBe('waiting');
	});

	it('reads done only after the upload and both passes', () => {
		expect(draftStep('done', 'done', 'done')).toEqual({
			name: 'Draft',
			detail: 'The draft is ready.',
			state: 'done',
			percent: 100
		});
	});

	it('reads failed when a pass or the upload stops', () => {
		expect(draftStep('failed', 'done', 'done').state).toBe('failed');
		expect(draftStep('done', 'failed', 'waiting').detail).toBe('A pass stopped, so the draft is not ready.');
		expect(draftStep('done', 'done', 'failed').state).toBe('failed');
	});
});

describe('readingFromJob', () => {
	it('does not drag a finished pass back to a catch-up frame', () => {
		const done: ProcessingStep = {
			name: 'Transcription',
			detail: 'Transcript ready.',
			state: 'done',
			percent: 100
		};
		const next = readingFromJob(done, 'queued', 'connecting', undefined, undefined, '', 'Transcript ready.', 'transcript');
		expect(next.state).toBe('done');
	});

	it('names an interrupted pass and a failed pass', () => {
		expect(stoppedDetail('editorial', 'interrupted', '')).toBe(
			'The editorial pass stopped when the server restarted. It does not rerun on its own.'
		);
		const failed = readingFromJob(waiting, 'error', 'live', undefined, undefined, 'budget refused', 'Transcript ready.', 'transcript');
		expect(failed.state).toBe('failed');
		expect(failed.detail).toBe('The transcript pass failed: budget refused.');
	});
});

describe('uploadDetail', () => {
	it('names the bytes the server has acknowledged', () => {
		expect(uploadDetail(0, 0)).toBe('Uploading the take.');
		expect(uploadDetail(10, 40)).toBe('Uploading the take. 10 of 40 bytes.');
		expect(uploadPercent(10, 40)).toBe(25);
		expect(uploadPercent(0, 0)).toBe(0);
	});
});
