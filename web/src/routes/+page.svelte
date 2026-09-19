<script lang="ts">
	import { browser } from '$app/environment';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { pageTitle } from '$lib/shell';
	import { formatClock, formatEpisodeNumber, GalleryController, initialGallery } from './threads/threads';

	let snap = $state(initialGallery());
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
	<nav aria-label="Season">
		<a href={resolve('/record')}>Record a new episode</a>
		<a href={resolve('/threads?fixture=1')}>Threads</a>
	</nav>

	{#if snap.ready}
		<ol aria-label="Episodes, newest first">
			{#each snap.season as episode (episode.id)}
				<li>
					{#if episode.state === 'rendering'}
						<article aria-label={`${episode.title}, rendering`}>
							<div class="cover" role="img" aria-label={`Cover of episode ${episode.number}`}>
								<span>{formatEpisodeNumber(episode.number)}</span>
							</div>
							<p class="state">Rendering</p>
							<h2>{episode.title}</h2>
							<p class="dur">{episode.date} · {formatClock(episode.duration)}</p>
							<div
								role="progressbar"
								aria-label={`Render progress for ${episode.title}`}
								aria-valuemin={0}
								aria-valuemax={100}
								aria-valuenow={controller ? controller.cardFor(episode.jobId).percent : 0}
							>
								<div
									class="bar"
									style={`width: ${controller ? controller.cardFor(episode.jobId).percent : 0}%`}
								></div>
							</div>
							<p class="job">{controller ? controller.cardFor(episode.jobId).detail : ''}</p>
							<a href={resolve(`/processing?episode=${episode.id}`)}>Open the processing screen</a>
						</article>
					{:else if episode.state === 'draft'}
						<a
							class="card"
							href={resolve(`/episode/${episode.id}/edit?fixture=1`)}
							aria-label={`${episode.title}, draft. Open in the editor.`}
						>
							<div class="cover" role="img" aria-label={`Cover of episode ${episode.number}`}>
								<span>{formatEpisodeNumber(episode.number)}</span>
							</div>
							<p class="state">Draft</p>
							<h2>{episode.title}</h2>
							<p class="dur">{episode.date} · {formatClock(episode.duration)}</p>
							<p class="job">In the editor · proposals waiting</p>
						</a>
					{:else}
						<a
							class="card"
							href={resolve(`/episode/${episode.id}?fixture=1`)}
							aria-label={`${episode.title}, ready. Open the episode.`}
						>
							<div class="cover" role="img" aria-label={`Cover of episode ${episode.number}`}>
								<span>{formatEpisodeNumber(episode.number)}</span>
							</div>
							<p class="state">{episode.published ? 'Public' : 'Private'}</p>
							<h2>{episode.title}</h2>
							<p class="dur">{episode.date} · {formatClock(episode.duration)}</p>
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
