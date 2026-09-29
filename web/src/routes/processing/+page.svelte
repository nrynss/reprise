<script lang="ts">
	import { resolve } from '$app/paths';
	import GalleryLink from '$lib/components/GalleryLink.svelte';
	import { pageTitle } from '$lib/shell';
	import {
		ProcessingController,
		emptyProcessing,
		statusWord,
		showsDetail,
		PROCESSING_SUB,
		PROCESSING_READY_NOTICE,
		STOPPED_STATUS,
		WAITING_STATUS
	} from '$lib/voice/processing-state';

	let snap = $state(emptyProcessing());
	let controller = $state<ProcessingController | null>(null);

	const stopped = $derived.by(
		() => [snap.upload, snap.transcription, snap.editorial, snap.draft].find((step) => step.state === 'failed') ??
			null
	);

	const steps = $derived([snap.upload, snap.transcription, snap.editorial, snap.draft]);

	// The overall line mirrors the first unfinished row, so a glance at
	// the top says where the take stands without opening any row.
	const overall = $derived.by(() => {
		if (snap.draft.state === 'done') return PROCESSING_READY_NOTICE;
		const rows = [snap.upload, snap.transcription, snap.editorial, snap.draft];
		if (rows.some((row) => row.state === 'failed')) return STOPPED_STATUS;
		const active = rows.find((row) => row.state === 'running') ??
			rows.find((row) => row.state === 'waiting');
		if (!active) return WAITING_STATUS;
		return statusWord(active);
	});

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
		content={PROCESSING_SUB}
	/>
</svelte:head>

<main>
	<GalleryLink />
	<p class="eyebrow">This take</p>
	<h1>Processing</h1>
	<p class="sub">{PROCESSING_SUB}</p>
	<p class="sub" role="status">{overall}</p>
	{#if snap.draft.state === 'done' && snap.episode !== ''}
		<nav aria-label="Season">
			<a class="button" href={resolve(`/episode/${snap.episode}`)}>Open the episode</a>
		</nav>
	{/if}

	{#if stopped}
		<section class="section" aria-label="Stopped">
			<h2>This step stopped</h2>
			<p role="alert">{stopped.detail}</p>
			{#if snap.transcription.state === 'failed' && snap.upload.state !== 'failed'}
				<div class="actions">
					<button type="button" onclick={() => controller?.retrySettle()}>Retry draft move</button>
				</div>
			{/if}
		</section>
	{/if}

	<ol class="steps" aria-label="Progress">
		{#each steps as step (step.name)}
			<li>
				<span class="step-name">{step.name}</span>
				<span class="step-status">{statusWord(step)}</span>
				{#if step.state === 'running'}
					<div
						class="bar-track"
						role="progressbar"
						aria-label={`${step.name} progress`}
						aria-valuemin={0}
						aria-valuemax={100}
						aria-valuenow={step.percent}
					>
						<div class="bar" style={`width: ${step.percent}%`}></div>
					</div>
				{/if}
				{#if showsDetail(step)}
					<p class="step-detail">{step.detail}</p>
				{/if}
			</li>
		{/each}
	</ol>
</main>

<style>
	/* The four processing rows. A flat list with no borders, so the
	section reads as one list and the status word carries each row. */
	.steps {
		list-style: none;
		padding: 0;
		margin: 2rem 0 0;
	}
	.steps li {
		display: flex;
		flex-wrap: wrap;
		align-items: baseline;
		gap: 0.25rem 1rem;
		padding-block: 0.75rem;
	}
	.step-name {
		font-weight: 600;
	}
	.step-status {
		margin-left: auto;
		color: var(--accent);
		font-weight: 600;
	}
	.step-detail {
		flex-basis: 100%;
		margin: 0;
		color: var(--muted);
	}
	.bar-track {
		flex-basis: 100%;
		height: 0.3rem;
		border-radius: 100px;
		background: var(--raised);
		overflow: hidden;
	}
	.bar {
		height: 100%;
		background: var(--accent);
		border-radius: 100px;
	}
</style>
