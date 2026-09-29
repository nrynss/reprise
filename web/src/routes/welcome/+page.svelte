<script lang="ts">
	import { browser } from '$app/environment';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import DemoBadge from '$lib/components/DemoBadge.svelte';
	import { pageTitle } from '$lib/shell';
	import {
		formatClock,
		initialWelcome,
		recordLabel,
		teaserEyebrow,
		TEASER_SECONDS,
		WelcomeController
	} from './welcome';

	let snap = $state(initialWelcome());
	let controller = $state<WelcomeController | null>(null);

	$effect(() => {
		const search = page.url.search;
		snap = initialWelcome(search);
		if (!browser) return;
		const next = new WelcomeController((fresh) => {
			snap = fresh;
		});
		controller = next;
		next.mount(search);
		return () => {
			next.destroy();
			controller = null;
		};
	});
</script>

<svelte:head>
	<title>{pageTitle('Welcome')}</title>
	<meta
		name="description"
		content="First visit. Hear thirty seconds of the season, then record the next episode."
	/>
</svelte:head>

<main>
	<p class="eyebrow">A personal podcast</p>
	<h1>Reprise</h1>
	{#if snap.demo}
		<DemoBadge />
	{/if}
	<p class="sub">
		You talk. A host asks. It becomes an episode. And the host remembers
		what you said last time.
	</p>
	<p role="status">{snap.notice}</p>

	{#if snap.mode === 'seeded'}
		<section class="section" aria-label="Hear what remembering sounds like">
			<p class="eyebrow">{teaserEyebrow(snap.teaser.episodeNumber)} · {snap.teaser.title}</p>
			<h2>Hear what remembering sounds like</h2>
			<button
				id="welcome-play"
				class="button"
				onclick={() => void controller?.togglePlay()}
				aria-label={snap.playing
					? `Pause episode ${snap.teaser.episodeNumber}`
					: snap.capped
						? `Play thirty seconds of episode ${snap.teaser.episodeNumber}`
						: `Play episode ${snap.teaser.episodeNumber}`}
			>
				{snap.playing ? 'Pause' : 'Play'}
			</button>
			<p id="welcome-position" role="status" aria-label="Teaser position">
				{#if snap.capped}
					{formatClock(snap.position)} of {formatClock(TEASER_SECONDS)}
				{:else}
					{formatClock(snap.position)}
				{/if}
			</p>
			{#if snap.teaser.lineA || snap.teaser.lineB}
				<blockquote>
					{#if snap.teaser.lineA}<p>{snap.teaser.lineA}</p>{/if}
					{#if snap.teaser.lineB}<p>{snap.teaser.lineB}</p>{/if}
				</blockquote>
			{/if}
		</section>
	{/if}

	<nav aria-label="Start recording">
		<a id="welcome-record" class="button" href={resolve('/record')}>
			{recordLabel(snap.mode, snap.teaser.episodeNumber)}
		</a>
	</nav>

	{#if snap.gateResult}
		<p id="gate-status" role="status">{snap.gateResult}</p>
	{/if}
</main>

<style>
	blockquote {
		border-left: 2px solid var(--accent);
		margin: 1.25rem 0 0;
		padding-left: 1rem;
		color: var(--muted);
		line-height: 1.6;
	}
	blockquote p {
		margin: 0 0 0.5rem;
	}
	nav {
		margin: 2rem 0;
	}
</style>
