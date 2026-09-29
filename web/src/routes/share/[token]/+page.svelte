<script lang="ts">
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { pageTitle } from '$lib/shell';
	import {
		absoluteCoverUrl,
		DEAD_HEADING,
		fetchShare,
		fixtureShare,
		formatEpisodeNumber,
		SHARE_DESC,
		SHARE_FOOTER,
		shareAudioUrl,
		shareByline,
		SHARE_MISSING,
		START_OWN
	} from './share';

	let phase = $state('loading');
	let title = $state('');
	let number = $state(0);
	let author = $state('');
	let audioUrl = $state('');
	let coverUrl = $state('');
	let coverAbsolute = $state('');
	let failure = $state('');

	// readFixture answers the fixture flag behind one query string. The
	// named states render with no server, so proofs run offline.
	function readFixture() {
		for (const [key, value] of page.url.searchParams) {
			if (key === 'fixture') return value;
		}
		return '';
	}

	async function load() {
		const fixture = readFixture();
		if (fixture === 'published' || fixture === 'plain') {
			const payload = fixtureShare(fixture === 'plain' ? '' : 'Mara');
			title = payload.title;
			number = payload.number;
			author = payload.author;
			audioUrl = shareAudioUrl(payload);
			coverUrl = payload.cover_path;
			coverAbsolute = absoluteCoverUrl(page.url.origin, payload.cover_path);
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
			author = payload.author;
			audioUrl = shareAudioUrl(payload);
			coverUrl = payload.cover_path;
			coverAbsolute = absoluteCoverUrl(page.url.origin, payload.cover_path);
			phase = 'ready';
		} catch (error) {
			if (error instanceof Error && error.message === SHARE_MISSING) {
				phase = 'missing';
				return;
			}
			failure = 'The shared episode did not load. Try again.';
			phase = 'failed';
		}
	}

	$effect(() => {
		void load();
	});
</script>

<svelte:head>
	<title>{title ? pageTitle(title) : pageTitle('Shared episode')}</title>
	<meta name="description" content={SHARE_DESC} />
	<meta property="og:title" content={title || 'Shared episode'} />
	<meta property="og:description" content={SHARE_DESC} />
	{#if coverAbsolute}
		<meta property="og:image" content={coverAbsolute} />
	{/if}
	<meta property="og:type" content="website" />
	<meta name="twitter:card" content="summary_large_image" />
</svelte:head>

<main class="narrow">
	{#if phase === 'loading'}
		<p role="status">Loading the shared episode…</p>
	{/if}

	{#if phase === 'missing'}
		<h1>{DEAD_HEADING}</h1>
		<footer>
			<p>{SHARE_FOOTER} <a href={resolve('/')}>{START_OWN}</a></p>
		</footer>
	{/if}

	{#if phase === 'failed'}
		<p role="alert">{failure}</p>
		<button class="button secondary" onclick={() => void load()}>Retry</button>
	{/if}

	{#if phase === 'ready'}
		<section class="player" aria-label="Shared episode">
			{#if coverUrl}
				<img src={coverUrl} alt={`Cover of episode ${number}`} width="512" height="512" />
			{/if}
			<h1>{title}</h1>
			<p>{formatEpisodeNumber(number)}</p>
			{#if shareByline(author)}
				<p>{shareByline(author)}</p>
			{/if}
			<audio controls src={audioUrl} aria-label={`Play ${title}`}></audio>
		</section>
		<footer>
			<p>{SHARE_FOOTER} <a href={resolve('/')}>{START_OWN}</a></p>
		</footer>
	{/if}
</main>

<style>
	.player {
		border: 1px solid var(--line);
		border-radius: 0.75rem;
		background: var(--raised);
		padding: 1.5rem;
	}
	img {
		max-width: 100%;
		height: auto;
		border-radius: 0.6rem;
	}
	audio {
		width: 100%;
		margin-top: 1.5rem;
	}
	footer {
		margin-top: 2.5rem;
		color: var(--muted);
	}
	footer a {
		color: var(--accent);
	}
</style>
