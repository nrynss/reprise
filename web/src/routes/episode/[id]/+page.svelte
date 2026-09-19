<script lang="ts">
	import { browser } from '$app/environment';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { pageTitle } from '$lib/shell';
	import { emptyEpisode, EpisodeController, formatClock, formatEpisodeNumber } from '../../threads/threads';

	let snap = $state(emptyEpisode());
	let controller = $state<EpisodeController | null>(null);

	$effect(() => {
		if (!browser) return;
		const next = new EpisodeController(page.params.id ?? 'missing', (fresh) => {
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
	<title>{snap.episode ? pageTitle(snap.episode.title) : pageTitle('Episode')}</title>
	<meta
		name="description"
		content="A finished episode: player, chapters, show notes, and a transcript that follows playback."
	/>
</svelte:head>

<main>
	{#if !snap.ready}
		<p role="status">Loading the episode.</p>
	{:else if !snap.episode}
		<h1>Missing episode</h1>
		<p role="status">{snap.notice}</p>
		<a href={resolve('/?fixture=1')}>Back to the gallery</a>
	{:else}
		<p class="eyebrow">{formatEpisodeNumber(snap.episode.number)} · {snap.episode.date}</p>
		<h1>{snap.episode.title}</h1>
		<p class="state">{snap.published ? 'Public' : 'Private'}</p>
		<p role="status">{snap.notice}</p>
		<nav aria-label="Season">
			<a href={resolve('/?fixture=1')}>Gallery</a>
			<a href={resolve('/threads?fixture=1')}>Threads</a>
		</nav>

		<section aria-label="Episode playback">
			<button
				onclick={() => void controller?.togglePlay()}
				aria-label={snap.playing ? 'Pause episode' : `Play ${snap.episode.title}`}
			>
				{snap.playing ? 'Pause' : 'Play'}
			</button>
			<button onclick={() => controller?.backFifteen()} aria-label="Back fifteen seconds">
				Back 15
			</button>
			<input
				type="range"
				min={0}
				max={snap.episode.duration}
				step={1}
				value={Math.round(snap.position)}
				oninput={(event) => controller?.seekTo(Number(event.currentTarget.value))}
				aria-label="Seek through the episode"
			/>
			<p role="status" aria-label="Playback position">
				{formatClock(snap.position)} of {formatClock(snap.episode.duration)}
			</p>
		</section>

		<section aria-label="Episode controls">
			<h2>Release</h2>
			{#if snap.published}
				<p>Fixture link: https://reprise.nryn.dev/e/{snap.episode.id}-fixture</p>
				<button onclick={() => controller?.publishState()}>Revoke link</button>
			{:else}
				<button onclick={() => controller?.publishState()}>Publish…</button>
			{/if}
			<button onclick={() => controller?.exportNotes()}>Export for YouTube</button>
			<button
				onclick={() => void controller?.erase()}
				aria-label={snap.eraseArmed ? 'Confirm erase' : 'Erase this episode'}
			>
				{snap.eraseArmed ? 'Confirm erase' : 'Erase this episode'}
			</button>
		</section>

		<section aria-label="Chapters">
			<h2>Chapters</h2>
			<ol>
				{#each snap.episode.chapters as chapter, index (chapter.start)}
					<li>
						<button
							onclick={() => {
								controller?.seekTo(chapter.start);
								void controller?.togglePlayIfPaused();
							}}
							aria-label={`Play chapter ${chapter.title} from ${formatClock(chapter.start)}`}
							aria-current={index === snap.activeChapter ? 'true' : undefined}
						>
							<span>{formatClock(chapter.start)}</span>
							<span>{chapter.title}</span>
						</button>
					</li>
				{/each}
			</ol>
		</section>

		<section aria-label="Show notes">
			<h2>Show notes</h2>
			<p>{snap.episode.notes}</p>
		</section>

		<section aria-label="Transcript, follows playback">
			<h2>Transcript</h2>
			{#each snap.words as word, index (index)}
				<button
					class:active={index === snap.activeWord}
					aria-current={index === snap.activeWord ? 'true' : undefined}
					aria-label={`${word.text} Activate to seek.`}
					onclick={() => controller?.seekWord(index)}
				>{word.text}</button>
			{/each}
		</section>
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
	.state {
		font-size: 0.75rem;
		letter-spacing: 0.1em;
		text-transform: uppercase;
		color: var(--accent);
	}
	nav {
		display: flex;
		gap: 1rem;
		margin: 1rem 0 2rem;
	}
	nav a {
		color: var(--accent);
	}
	section {
		background: var(--raised);
		border: 1px solid var(--line);
		border-radius: 0.75rem;
		padding: 1.25rem 1.5rem;
		margin-bottom: 1.25rem;
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
		margin-right: 0.5rem;
		margin-bottom: 0.5rem;
	}
	section[aria-label='Transcript, follows playback'] button {
		background: transparent;
		color: var(--muted);
		border: none;
		border-radius: 0.25rem;
		padding: 0.1rem 0.15rem;
		margin: 0 0.3rem 0.15rem 0;
		font-weight: 400;
	}
	section[aria-label='Transcript, follows playback'] button.active {
		background: var(--accent);
		color: var(--on-accent);
	}
	section[aria-label='Chapters'] button {
		background: transparent;
		color: var(--muted);
		border: none;
		border-radius: 0.4rem;
		padding: 0.5rem 0.25rem;
		margin: 0;
		font-weight: 400;
		display: flex;
		gap: 0.9rem;
		width: 100%;
		text-align: left;
	}
	section[aria-label='Chapters'] button span:first-child {
		color: var(--accent);
	}
	ol {
		list-style: none;
		padding: 0;
		margin: 0;
	}
	li {
		border-bottom: 1px solid var(--line);
	}
	li:last-child {
		border-bottom: none;
	}
	input[type='range'] {
		width: 100%;
		margin: 0.75rem 0;
		accent-color: var(--accent);
	}
	p {
		color: var(--muted);
		line-height: 1.65;
	}
</style>
