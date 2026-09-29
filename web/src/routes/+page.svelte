<script lang="ts">
	import { browser } from '$app/environment';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import DemoBadge from '$lib/components/DemoBadge.svelte';
	import SeasonNav from '$lib/components/SeasonNav.svelte';
	import { pageTitle } from '$lib/shell';
	import {
		emptyGallery,
		formatEpisodeNumber,
		galleryCardKind,
		GalleryController,
		jobCardHref,
		seasonHref
	} from './threads/threads';

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
	<meta name="description" content="Your episodes, newest first." />
</svelte:head>

<main>
	<p class="eyebrow">Season one</p>
	<h1>Your episodes</h1>
	<p class="sub">Only you can see them until you publish.</p>
	{#if !snap.live}
		<DemoBadge />
	{/if}
	{#if snap.notice}
		<p role="status">{snap.notice}</p>
	{/if}
	{#if snap.failed}
		<button onclick={() => controller?.retry()}>Retry the season</button>
	{/if}
	<nav aria-label="Season">
		<a class="button" href={resolve('/record')}>Record a new episode</a>
		<SeasonNav current="gallery" />
	</nav>

	{#if snap.ready && snap.rows.length === 0 && !snap.failed}
		<section class="section" aria-label="Empty season">
			<h2>No episodes yet</h2>
			<p>Record the first one and it lands here, newest first.</p>
			<a class="button" href={resolve('/record')}>Record the first episode</a>
		</section>
	{/if}

	{#if snap.ready && snap.rows.length > 0}
		<ol class="cards" aria-label="Episodes, newest first">
			{#each snap.rows as row (row.id)}
				{@const kind = galleryCardKind(row, controller && row.jobId ? controller.cardFor(row.jobId) : null)}
				<li>
					{#if kind === 'editor'}
						<a
							class="card"
							href={resolve(seasonHref(row))}
							aria-label={`${row.title}, draft. Open in the editor.`}
						>
							{#if row.coverPath}
								<div class="cover">
									<img
										src={row.coverPath}
										alt={`Cover of episode ${row.number}`}
										width="512"
										height="512"
									/>
								</div>
							{:else}
								<div class="cover" role="img" aria-label={`Cover of episode ${row.number}`}>
									<span>{formatEpisodeNumber(row.number)}</span>
								</div>
							{/if}
							<p class="state">Draft</p>
							<h2 class="title">{row.title}</h2>
							<p class="dur">{row.meta}</p>
							{#if row.fixture}
								<p class="job">Draft waiting in the editor</p>
							{/if}
						</a>
					{:else if kind === 'job'}
						<article class="card" aria-label={`${row.title}, ${row.state}`}>
							{#if row.coverPath}
								<div class="cover">
									<img
										src={row.coverPath}
										alt={`Cover of episode ${row.number}`}
										width="512"
										height="512"
									/>
								</div>
							{:else}
								<div class="cover" role="img" aria-label={`Cover of episode ${row.number}`}>
									<span>{formatEpisodeNumber(row.number)}</span>
								</div>
							{/if}
							<p class="state">{row.state}</p>
							<h2 class="title">{row.title}</h2>
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
								<a href={resolve(jobCardHref(row))}>Open the processing screen</a>
							{:else}
								<a href={resolve(jobCardHref(row))}>Open the episode</a>
							{/if}
						</article>
					{:else}
						<a
							class="card"
							href={resolve(seasonHref(row))}
							aria-label={`${row.title}, ${row.state}. Open the episode.`}
						>
							{#if row.coverPath}
								<div class="cover">
									<img
										src={row.coverPath}
										alt={`Cover of episode ${row.number}`}
										width="512"
										height="512"
									/>
								</div>
							{:else}
								<div class="cover" role="img" aria-label={`Cover of episode ${row.number}`}>
									<span>{formatEpisodeNumber(row.number)}</span>
								</div>
							{/if}
							<p class="state">{row.state}</p>
							<h2 class="title">{row.title}</h2>
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
	.state {
		font-size: 0.75rem;
		letter-spacing: 0.1em;
		text-transform: uppercase;
		color: var(--accent);
		margin: 0 0 0.35rem;
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
	.card a {
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
