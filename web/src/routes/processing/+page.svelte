<script lang="ts">
	import { resolve } from '$app/paths';
	import { pageTitle } from '$lib/shell';
	import { ProcessingController, emptyProcessing } from '$lib/voice/processing-state';

	let snap = $state(emptyProcessing());
	let controller = $state<ProcessingController | null>(null);

	const stopped = $derived.by(
		() => [snap.upload, snap.transcription, snap.editorial, snap.draft].find((step) => step.state === 'failed') ??
			null
	);

	$effect(() => {
		const next = new ProcessingController(new URLSearchParams(window.location.search), (fresh) => {
			snap = fresh;
		});
		controller = next;
		next.mount();
		return () => {
			controller = null;
			next.destroy();
		};
	});
</script>

<svelte:head>
	<title>{pageTitle('Processing')}</title>
	<meta
		name="description"
		content="The take stays here until the draft is ready. Upload leads, then the transcript and the editorial pass."
	/>
</svelte:head>

<main>
	<p class="eyebrow">This take</p>
	<h1>Processing</h1>
	<p class="sub">The take stays here until the draft is ready.</p>
	<p role="status">
		{snap.draft.state === 'done' ? 'The draft is ready.' : 'The take stays here until the draft is ready.'}
	</p>
	<nav aria-label="Season">
		<a href={resolve('/')}>Gallery</a>
		{#if snap.draft.state === 'done' && snap.episode !== ''}
			<a href={resolve(`/episode/${snap.episode}`)}>Open the episode</a>
		{/if}
	</nav>

	{#if stopped}
		<section aria-label="Stopped">
			<h2>This step stopped</h2>
			<p role="alert">{stopped.detail}</p>
			{#if snap.transcription.state === 'failed' && snap.upload.state !== 'failed'}
				<button type="button" onclick={() => controller?.retrySettle()}>Retry draft move</button>
			{/if}
		</section>
	{/if}

	<ol aria-label="Session jobs">
		{#each [snap.upload, snap.transcription, snap.editorial, snap.draft] as step (step.name)}
			<li>
				<p class="state">{step.state}</p>
				<h2>{step.name}</h2>
				<p>{step.state}: {step.detail}</p>
				{#if step.state === 'running'}
					<div
						role="progressbar"
						aria-label={`${step.name} progress`}
						aria-valuemin={0}
						aria-valuemax={100}
						aria-valuenow={step.percent}
					>
						<div class="bar" style={`width: ${step.percent}%`}></div>
					</div>
				{/if}
			</li>
		{/each}
	</ol>
</main>

<style>
	:root {
		color-scheme: dark;
		--paper: #191410;
		--raised: #241d15;
		--ink: #f4edde;
		--muted: #d9cfbb;
		--accent: #e8a33d;
		--on-accent: #201809;
		--line: #5a4f41;
	}
	main {
		max-width: 44rem;
		margin: 0 auto;
		padding: 3rem 1.5rem 5rem;
		font-family: system-ui, sans-serif;
		background: var(--paper);
		color: var(--ink);
	}
	.eyebrow {
		font-size: 0.75rem;
		letter-spacing: 0.12em;
		text-transform: uppercase;
		color: var(--accent);
		margin: 0 0 0.5rem;
	}
	h1 {
		font-size: 2rem;
		margin: 0 0 0.5rem;
		color: var(--ink);
	}
	.sub,
	p {
		color: var(--muted);
		line-height: 1.65;
	}
	nav {
		display: flex;
		gap: 1rem;
		margin: 1rem 0 2rem;
	}
	nav a {
		color: var(--accent);
	}
	section,
	li {
		background: var(--raised);
		border: 1px solid var(--line);
		border-radius: 0.75rem;
		padding: 1.25rem 1.5rem;
		margin-bottom: 1.25rem;
	}
	ol {
		list-style: none;
		padding: 0;
		margin: 0;
	}
	.state {
		font-size: 0.75rem;
		letter-spacing: 0.1em;
		text-transform: uppercase;
		color: var(--accent);
		margin: 0 0 0.35rem;
	}
	h2 {
		font-size: 1.1rem;
		margin: 0 0 0.75rem;
		color: var(--ink);
	}
	button {
		background: var(--accent);
		color: var(--on-accent);
		border: none;
		border-radius: 100px;
		padding: 0.55rem 1.1rem;
		font-weight: 600;
	}
	div[role='progressbar'] {
		height: 0.3rem;
		border-radius: 100px;
		background: var(--paper);
		overflow: hidden;
		margin-top: 0.6rem;
	}
	.bar {
		height: 100%;
		background: var(--accent);
		border-radius: 100px;
	}
</style>
