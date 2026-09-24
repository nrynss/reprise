<script lang="ts">
	import { browser } from '$app/environment';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { pageTitle } from '$lib/shell';
	import { emptyGallery, formatEpisodeNumber, GalleryController, seasonHref } from './threads/threads';

	let snap = $state(emptyGallery());
	let controller = $state<GalleryController | null>(null);

	$effect(() => {
		if (!browser) return;
		const next = new GalleryController((fresh) => {
			snap = fresh;
		});
		controller = next;
		next.mount(page.url.search);
		return () => {
			next.destroy();
			controller = null;
		};
	});
</script>

<svelte:head>
	<title>{pageTitle('Gallery')}</title>
	<meta
		name="description"
		content="The season so far. Episodes newest first, with live progress on any running job."
	/>
</svelte:head>

<main>
	<p class="eyebrow">Season one</p>
	<h1>The season so far</h1>
	<p class="sub">
		Episodes are private until published. A running job shows its progress
		right on the card, through the same job stream the processing screen reads.
	</p>
	<p role="status">{snap.notice}</p>
	{#if snap.failed}
		<button onclick={() => controller?.retry()}>Retry the season</button>
	{/if}
	<nav aria-label="Season">
		<a href={resolve('/record')}>Record a new episode</a>
		<a href={resolve('/threads')}>Threads</a>
	</nav>

	{#if snap.ready && snap.rows.length === 0 && !snap.failed}
		<section aria-label="Empty season">
			<h2>No episodes yet</h2>
			<p>Record the first one and it lands here, newest first.</p>
			<a href={resolve('/record')}>Record the first episode</a>
		</section>
	{/if}

	{#if snap.ready && snap.rows.length > 0}
		<ol aria-label="Episodes, newest first">
			{#each snap.rows as row (row.id)}
				<li>
					{#if row.state === 'draft'}
						<a
							class="card"
							href={resolve(seasonHref(row))}
							aria-label={`${row.title}, draft. Open in the editor.`}
						>
							<div class="cover" role="img" aria-label={`Cover of episode ${row.number}`}>
								<span>{formatEpisodeNumber(row.number)}</span>
							</div>
							<p class="state">Draft</p>
							<h2>{row.title}</h2>
							<p class="dur">{row.meta}</p>
							{#if row.fixture}
								<p class="job">In the editor · proposals waiting</p>
							{/if}
						</a>
					{:else if row.jobId}
						<article aria-label={`${row.title}, ${row.state}`}>
							<div class="cover" role="img" aria-label={`Cover of episode ${row.number}`}>
								<span>{formatEpisodeNumber(row.number)}</span>
							</div>
							<p class="state">{row.state}</p>
							<h2>{row.title}</h2>
							<p class="dur">{row.meta}</p>
							<div
								role="progressbar"
								aria-label={`Job progress for ${row.title}`}
								aria-valuemin={0}
								aria-valuemax={100}
								aria-valuenow={controller ? controller.cardFor(row.jobId).percent : 0}
							>
								<div
									class="bar"
									style={`width: ${controller ? controller.cardFor(row.jobId).percent : 0}%`}
								></div>
							</div>
							<p class="job">{controller ? controller.cardFor(row.jobId).detail : ''}</p>
							{#if row.fixture && row.state === 'rendering'}
								<a href={resolve(seasonHref(row))}>Open the processing screen</a>
							{:else}
								<a href={resolve(seasonHref(row))}>Open the episode</a>
							{/if}
						</article>
					{:else}
						<a
							class="card"
							href={resolve(seasonHref(row))}
							aria-label={`${row.title}, ${row.state}. Open the episode.`}
						>
							<div class="cover" role="img" aria-label={`Cover of episode ${row.number}`}>
								<span>{formatEpisodeNumber(row.number)}</span>
							</div>
							<p class="state">{row.state}</p>
							<h2>{row.title}</h2>
							<p class="dur">{row.meta}</p>
						</a>
					{/if}
				</li>
			{/each}
		</ol>
	{/if}

	{#if snap.gateResult}
		<p id="gate-status" role="status">{snap.gateResult}</p>
	{/if}
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
		max-width: 52rem;
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
	.sub {
		color: var(--muted);
		line-height: 1.6;
		max-width: 40rem;
	}
	nav {
		display: flex;
		gap: 1rem;
		margin: 1.5rem 0;
	}
	nav a {
		color: var(--on-accent);
		background: var(--accent);
		border-radius: 100px;
		padding: 0.6rem 1.25rem;
		text-decoration: none;
		font-weight: 600;
	}
	nav a:last-child {
		background: transparent;
		color: var(--accent);
		border: 1px solid var(--line);
	}
	section[aria-label='Empty season'] {
		background: var(--raised);
		border: 1px solid var(--line);
		border-radius: 0.75rem;
		padding: 1.5rem;
		margin-bottom: 1.25rem;
	}
	section[aria-label='Empty season'] a {
		color: var(--accent);
	}
	button {
		background: var(--accent);
		color: var(--on-accent);
		border: none;
		border-radius: 100px;
		padding: 0.55rem 1.1rem;
		font-weight: 600;
		margin-bottom: 1rem;
	}
	ol {
		list-style: none;
		padding: 0;
		margin: 0;
		display: grid;
		grid-template-columns: repeat(auto-fill, minmax(16rem, 1fr));
		gap: 1.25rem;
	}
	.card,
	article {
		display: block;
		background: var(--raised);
		border: 1px solid var(--line);
		border-radius: 0.75rem;
		padding: 1rem;
		color: var(--ink);
		text-decoration: none;
	}
	.card:hover {
		border-color: var(--accent);
	}
	.cover {
		aspect-ratio: 1;
		border-radius: 0.6rem;
		display: flex;
		align-items: center;
		justify-content: center;
		background: var(--paper);
		border: 1px solid var(--line);
		margin-bottom: 0.75rem;
	}
	.cover span {
		color: var(--accent);
		font-size: 0.85rem;
		letter-spacing: 0.12em;
	}
	.state {
		font-size: 0.75rem;
		letter-spacing: 0.1em;
		text-transform: uppercase;
		color: var(--accent);
		margin: 0 0 0.35rem;
	}
	h2 {
		font-size: 1.25rem;
		margin: 0 0 0.25rem;
		color: var(--ink);
	}
	.dur {
		color: var(--muted);
		font-size: 0.85rem;
		margin: 0 0 0.5rem;
	}
	.job {
		color: var(--muted);
		font-size: 0.85rem;
		margin: 0.5rem 0 0;
	}
	article a {
		color: var(--accent);
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
