<script lang="ts">
	import { pageTitle } from '$lib/shell';
	import { RecordController, emptySnapshot } from '$lib/voice/record-state';

	let snap = $state(emptySnapshot);

	$effect(() => {
		const params = new URLSearchParams(window.location.search);
		const controller = new RecordController({
			mock: params.get('mock') === '1',
			resume: params.get('resume') === '1',
			onChange: (next) => {
				snap = next;
			}
		});
		controller.mount();
		const startButton = document.getElementById('record-start');
		const endButton = document.getElementById('record-end');
		const onStart = () => {
			void controller.start();
		};
		const onEnd = () => {
			controller.endControl();
		};
		startButton?.addEventListener('click', onStart);
		endButton?.addEventListener('click', onEnd);
		return () => {
			startButton?.removeEventListener('click', onStart);
			endButton?.removeEventListener('click', onEnd);
			controller.destroy();
		};
	});
</script>

<svelte:head>
	<title>{pageTitle(snap.phase === 'live' ? 'On air' : 'Record')}</title>
</svelte:head>

<main>
	<h1>{snap.phase === 'live' ? 'On air' : 'Record'}</h1>
	<p role="status">{snap.notice}</p>

	{#if snap.phase === 'preflight' || snap.phase === 'starting'}
		<section aria-label="Session checks">
			<h2>Before you start</h2>
			<ul>
				<li>Microphone: granted when you press start.</li>
				<li>Connection: opens with the session request.</li>
				<li>
					Memory: {snap.greeting.length > 0
						? snap.greeting
						: 'the host greets you once the session opens.'}
				</li>
			</ul>
			<button id="record-start" disabled={snap.phase !== 'preflight'} aria-label="Start session">
				{snap.phase === 'starting' ? 'Opening…' : 'Start session'}
			</button>
		</section>
	{/if}

	{#if snap.phase === 'live' || snap.phase === 'ending'}
		<section aria-label="Live session">
			<h2>Live session</h2>
			<p aria-label="Elapsed time">{snap.elapsed}</p>
			<p
				aria-label="Input level"
				aria-valuenow={Math.round(snap.levelDb)}
				role="meter"
				aria-valuemin={-100}
				aria-valuemax={0}
			>
				Input level {Math.round(snap.levelDb)} dB
			</p>
			{#if snap.greeting.length > 0}
				<blockquote>{snap.greeting}</blockquote>
			{/if}
			<ol aria-label="Conversation" aria-live="polite">
				{#each snap.turns as turn, index (index)}
					<li><strong>{turn.role === 'host' ? 'Host' : 'You'}:</strong> {turn.text}</li>
				{/each}
			</ol>
			<button
				id="record-end"
				disabled={snap.phase !== 'live'}
				aria-label={snap.armed ? 'Confirm end session' : 'End session'}
			>
				{snap.armed ? 'Confirm end session' : 'End session'}
			</button>
		</section>
	{/if}

	{#if snap.phase === 'recovering'}
		<section aria-label="Recovered upload">
			<h2>Recovered upload</h2>
		</section>
	{/if}
</main>

<style>
	main {
		max-width: 40rem;
		margin: 0 auto;
		padding: 4rem 1.5rem;
		font-family: system-ui, sans-serif;
	}
	blockquote {
		border-left: 2px solid currentColor;
		padding-left: 1rem;
	}
</style>
