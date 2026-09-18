<script lang="ts">
	import { pageTitle } from '$lib/shell';
	import { ProcessingController, emptyProcessing } from '$lib/voice/processing-state';

	let snap = $state(emptyProcessing());

	$effect(() => {
		const controller = new ProcessingController(new URLSearchParams(window.location.search), (next) => {
			snap = next;
		});
		controller.mount();
		return () => {
			controller.destroy();
		};
	});
</script>

<svelte:head>
	<title>{pageTitle('Processing')}</title>
</svelte:head>

<main>
	<h1>Processing</h1>
	<p role="status">The take stays here until the draft is ready.</p>
	<ol aria-label="Session jobs">
		{#each [snap.upload, snap.transcription, snap.editorial, snap.draft] as step (step.name)}
			<li>
				<h2>{step.name}</h2>
				<p>{step.state}: {step.detail}</p>
			</li>
		{/each}
	</ol>
</main>

<style>
	main {
		max-width: 40rem;
		margin: 0 auto;
		padding: 4rem 1.5rem;
		font-family: system-ui, sans-serif;
	}
	ol {
		list-style: none;
		padding: 0;
	}
	li {
		border-bottom: 1px solid currentColor;
		padding: 1rem 0;
	}
</style>
