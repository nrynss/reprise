<script lang="ts">
	import { page } from '$app/state';
	import { pageTitle } from '$lib/shell';
	import {
		fetchShare,
		fixtureShare,
		formatEpisodeNumber,
		shareAudioUrl,
		SHARE_MISSING
	} from './share';

	let phase = $state('loading');
	let title = $state('');
	let number = $state(0);
	let audioUrl = $state('');
	let coverUrl = $state('');
	let failure = $state('');

	async function load() {
		const fixture = page.url.searchParams.get('fixture');
		if (fixture === 'published') {
			const payload = fixtureShare();
			title = payload.title;
			number = payload.number;
			audioUrl = shareAudioUrl(payload);
			coverUrl = payload.cover_path;
			phase = 'ready';
			return;
		}
		if (fixture === 'revoked') {
			phase = 'missing';
			return;
		}
		phase = 'loading';
		failure = '';
		try {
			const payload = await fetchShare(fetch, page.params.token ?? '');
			title = payload.title;
			number = payload.number;
			audioUrl = shareAudioUrl(payload);
			coverUrl = payload.cover_path;
			phase = 'ready';
		} catch (error) {
			if (error instanceof Error && error.message === SHARE_MISSING) {
				phase = 'missing';
				return;
			}
			failure = error instanceof Error ? error.message : 'the shared episode could not load';
			phase = 'failed';
		}
	}

	$effect(() => {
		void load();
	});
</script>

<svelte:head>
	<title>{title ? pageTitle(title) : pageTitle('Shared episode')}</title>
	<meta
		name="description"
		content="A shared episode. Only the finished audio is public, and everything behind it stays private."
	/>
</svelte:head>

<main>
	<p>Shared from a personal podcast</p>

	{#if phase === 'loading'}
		<p role="status">Loading the shared episode…</p>
	{/if}

	{#if phase === 'missing'}
		<section aria-label="Missing link">
			<h1>This link opens nothing</h1>
			<p>
				The author may have revoked it, or it never opened anything. Nothing
				behind a private episode ever leaks through a dead link.
			</p>
		</section>
	{/if}

	{#if phase === 'failed'}
		<p role="alert">The shared episode failed: {failure}</p>
		<button onclick={() => void load()}>Retry</button>
	{/if}

	{#if phase === 'ready'}
		<section aria-label="Shared episode">
			<img src={coverUrl} alt={`Cover of episode ${number}`} width="512" height="512" />
			<h1>{title}</h1>
			<p>{formatEpisodeNumber(number)}</p>
			<audio controls src={audioUrl} aria-label={`Play ${title}`}></audio>
		</section>
		<footer>
			<p>Reprise</p>
			<p>
				Published by the author. Only the finished episode is public, and
				everything behind it stays private.
			</p>
		</footer>
	{/if}
</main>

<style>
	main {
		max-width: 40rem;
		margin: 0 auto;
		padding: 4rem 1.5rem;
		font-family: system-ui, sans-serif;
	}
	img {
		max-width: 100%;
		height: auto;
		border-radius: 0.75rem;
	}
	audio {
		width: 100%;
		margin-top: 1.5rem;
	}
	footer {
		margin-top: 3rem;
		color: #6b6259;
		font-size: 0.85rem;
	}
</style>
