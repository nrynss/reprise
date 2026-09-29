<script lang="ts">
	import { pageTitle } from '$lib/shell';
	import GalleryLink from '$lib/components/GalleryLink.svelte';
	import { RecordController, emptySnapshot } from '$lib/voice/record-state';
	import {
		createCompletionDriver,
		exposeStemsMock
	} from './stems-complete';

	let snap = $state(emptySnapshot);
	let completionVisible = $state(false);
	let completionText = $state('');
	let completionFailed = $state(false);
	let retryReady = $state(false);
	let activeController = $state<RecordController | null>(null);

	const completionDriver = createCompletionDriver((view) => {
		completionVisible = true;
		completionText = view.text;
		completionFailed = view.status === 'failed';
		retryReady = view.canRetry;
	});

	function retryCompletion() {
		void completionDriver.retry();
	}

	function retryControllerCompletion() {
		void activeController?.retryCompletion();
	}

	$effect(() => {
		const params = Object.fromEntries(new URLSearchParams(window.location.search).entries());
		const controller = new RecordController({
			mock: params['mock'] === '1',
			resume: params['resume'] === '1',
			onChange: (next) => {
				snap = next;
			}
		});
		controller.mount();
		activeController = controller;
		if (params['mock'] === '1') {
			exposeStemsMock(completionDriver, () => controller.harness());
		}
		// The live section renders after the take opens, so the mount time
		// tree holds no end button yet. The document listener below reaches
		// the live rendered end control and the warn button alike. A native
		// button fires click on mouse press and on keyboard Enter and Space,
		// so delegation covers pointer and keyboard alike.
		const clicks = new AbortController();
		document.addEventListener(
			'click',
			(event) => {
				const target = event.target;
				if (target instanceof Element && target.closest('#stems-retry') !== null) {
					retryCompletion();
				} else if (target instanceof Element && target.closest('#record-retry') !== null) {
					retryControllerCompletion();
				} else if (target instanceof Element && target.closest('#record-end-cancel') !== null) {
					controller.cancelEnd();
				} else if (target instanceof Element && target.closest('#record-end-now') !== null) {
					controller.endControl();
				} else if (target instanceof Element && target.closest('#record-end') !== null) {
					controller.endControl();
				}
			},
			{ signal: clicks.signal }
		);
		return () => {
			clicks.abort();
			activeController = null;
			controller.destroy();
		};
	});
</script>

<svelte:head>
	<title>{pageTitle(snap.phase === 'live' ? 'On air' : 'Record')}</title>
</svelte:head>

<main>
	<GalleryLink />
	<h1>{snap.phase === 'live' ? 'On air' : 'Record'}</h1>
	<p class="sub" role="status">{snap.notice}</p>

	{#if snap.phase === 'preflight' || snap.phase === 'starting'}
		<div class="actions start-wrap">
			<button
				id="record-start"
				class="button start-big"
				disabled={activeController === null || snap.phase !== 'preflight'}
				onclick={() => void activeController?.start()}
				aria-label="Start session"
			>
				{snap.phase === 'starting' ? 'Opening…' : 'Start session'}
			</button>
		</div>
	{/if}

	{#if snap.phase === 'live' || snap.phase === 'ending'}
		<section class="section" aria-label="Live session">
			<h2>Live session</h2>
			<p class="clock" aria-label="Elapsed time">{snap.elapsed}</p>
			<p
				aria-label="Input level"
				aria-valuenow={Math.round(snap.levelDb)}
				role="meter"
				aria-valuemin={-100}
				aria-valuemax={0}
			>
				Input level {Math.round(snap.levelDb)} dB
			</p>
			<div class="actions">
				<button
					id="record-end"
					disabled={snap.phase !== 'live'}
					aria-label={snap.armed ? 'Confirm end session' : 'End session'}
				>
					{snap.armed ? 'Confirm end session' : 'End session'}
				</button>
				{#if snap.armed && snap.phase === 'live'}
					<button
						id="record-end-cancel"
						class="button secondary"
						type="button"
						aria-label="Cancel end">Cancel</button
					>
				{/if}
			</div>
			{#if snap.capWarning && snap.phase === 'live'}
				<div role="alert">
					<p>{snap.capText}</p>
					<button id="record-end-now" aria-label="End session now">End session now</button>
				</div>
			{/if}
			{#if snap.greeting.length > 0}
				<blockquote>{snap.greeting}</blockquote>
			{/if}
			<ol class="turns" aria-label="Conversation" aria-live="polite">
				{#each snap.turns as turn, index (index)}
					<li><strong>{turn.role === 'host' ? 'Host' : 'You'}:</strong> {turn.text}</li>
				{/each}
			</ol>
		</section>
	{/if}

	{#if snap.phase === 'ending' && snap.completionFailed}
		<section class="section" aria-label="Draft move retry">
			<h2>Draft move retry</h2>
			<div class="actions">
				<button id="record-retry" aria-label="Retry draft move">Retry draft move</button>
			</div>
		</section>
	{/if}

	{#if snap.phase === 'recovering'}
		<section class="section" aria-label="Recovered upload">
			<h2>Recovered upload</h2>
		</section>
	{/if}

	{#if completionVisible}
		<section class="section" aria-label="Draft move">
			<h2>Draft move</h2>
			{#if completionFailed}
				<div role="alert">
					<p>{completionText}</p>
					<div class="actions">
						<button
							id="stems-retry"
							disabled={!retryReady}
							aria-label="Retry draft move"
						>
							Retry draft move
						</button>
					</div>
				</div>
			{:else}
				<p role="status">{completionText}</p>
			{/if}
		</section>
	{/if}
</main>

<style>
	/* The one large start control. The shared primary pill carries the
	shape, and this size makes it the clear next move. The wrap centres
	it, and the shared actions group stacks it full width on a phone. */
	.start-wrap {
		justify-content: center;
		margin-block: 2.5rem;
	}
	.start-big {
		font-size: 1.25rem;
		padding: 1rem 2.75rem;
		min-height: 3.5rem;
	}
	/* The live clock. Large tabular figures hold still as they tick,
	so the time reads at a glance in the hand. */
	.clock {
		font-size: clamp(2.5rem, 2rem + 8vw, 4rem);
		font-weight: 700;
		font-variant-numeric: tabular-nums;
		line-height: 1.1;
		margin: 0 0 0.5rem;
		max-width: none;
	}
	.turns {
		list-style: none;
		padding: 0;
		margin: 1.5rem 0 0;
	}
	.turns li {
		max-width: 65ch;
		margin-block: 0.5rem;
	}
	blockquote {
		border-left: 2px solid currentColor;
		padding-left: 1rem;
		max-width: 65ch;
	}
</style>
