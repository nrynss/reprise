<script lang="ts">
	import { browser } from '$app/environment';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { pageTitle } from '$lib/shell';
	import {
		draftEditHref,
		emptyScreen,
		EpisodeController,
		formatClock,
		formatEpisodeNumber
	} from '../../threads/threads';

	let snap = $state(emptyScreen('missing'));
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
	<title>{snap.title ? pageTitle(snap.title) : pageTitle('Episode')}</title>
	<meta
		name="description"
		content="One episode: player, chapters, show notes, and a transcript that follows playback."
	/>
</svelte:head>

<main>
	{#if !snap.ready}
		<p role="status">Loading the episode.</p>
	{:else if snap.missing}
		<h1>Missing episode</h1>
		<p role="status">{snap.notice}</p>
		<a href={resolve('/')}>Back to the gallery</a>
	{:else if snap.failed}
		<h1>Episode refused</h1>
		<p role="status">{snap.notice}</p>
		<button onclick={() => controller?.retry()}>Retry the episode</button>
		<a href={resolve('/')}>Back to the gallery</a>
	{:else}
		<p class="eyebrow">{formatEpisodeNumber(snap.number)} · {snap.state} · {snap.visibility}</p>
		<h1>{snap.title}</h1>
		<p class="state">{snap.published ? 'Public' : 'Private'}</p>
		<p role="status">{snap.notice}</p>
		<nav aria-label="Season">
			{#if snap.live}
				{@const editHref = draftEditHref(snap)}
				<a href={resolve('/')}>Gallery</a>
				<a href={resolve('/threads')}>Threads</a>
				{#if editHref}
					<a href={resolve(editHref)}>Edit</a>
				{/if}
			{:else}
				<a href={resolve('/?fixture=1')}>Gallery</a>
				<a href={resolve('/threads?fixture=1')}>Threads</a>
			{/if}
		</nav>

		{#if snap.audioUrl && snap.duration !== null}
			<section aria-label="Episode playback">
				<button
					onclick={() => void controller?.togglePlay()}
					aria-label={snap.playing ? 'Pause episode' : `Play ${snap.title}`}
				>
					{snap.playing ? 'Pause' : 'Play'}
				</button>
				<button onclick={() => controller?.backFifteen()} aria-label="Back fifteen seconds">
					Back 15
				</button>
				<input
					type="range"
					min={0}
					max={snap.duration}
					step={1}
					value={Math.round(snap.position)}
					oninput={(event) => controller?.seekTo(Number(event.currentTarget.value))}
					aria-label="Seek through the episode"
				/>
				<p role="status" aria-label="Playback position">
					{formatClock(snap.position)} of {formatClock(snap.duration)}
				</p>
				{#if snap.live && snap.words.length === 0}
					<p>No stored words sit on this render yet, so no transcript follows playback.</p>
				{/if}
			</section>
		{:else if snap.live}
			<section aria-label="Episode playback">
				<h2>Playback</h2>
				<p>
					The detail carries no audio stream address yet, so playback waits
					here. The proposals and the quoted moment below still read.
				</p>
			</section>
		{/if}

		{#if snap.outcome}
			<section aria-label="Latest transcript pass">
				<h2>Transcript pass</h2>
				<p>State: {snap.outcome.status}</p>
				{#if snap.outcome.error}
					<p>Error: {snap.outcome.error}</p>
				{/if}
				<p class="dim">Job {snap.outcome.jobId}</p>
			</section>
		{/if}

		<section aria-label="Episode controls">
			<h2>Release</h2>
			{#if snap.published}
				{#if !snap.live}
					<p>Fixture link: https://reprise.nryn.dev/e/{snap.id}-fixture</p>
				{/if}
				<button onclick={() => controller?.publishState()}>Revoke link</button>
			{:else}
				<button onclick={() => controller?.publishState()}>Publish…</button>
			{/if}
			{#if snap.live}
				{#if snap.state === 'ready'}
					<button
						onclick={() => void controller?.exportBundle()}
						disabled={snap.exporting}
					>
						{snap.exporting ? 'Building bundle…' : 'Export bundle'}
					</button>
				{/if}
			{:else}
				<button onclick={() => controller?.exportNotes()}>Export for YouTube</button>
			{/if}
			<button
				onclick={() => void controller?.erase()}
				aria-label={snap.eraseArmed ? 'Confirm erase' : 'Erase this episode'}
			>
				{snap.eraseArmed ? 'Confirm erase' : 'Erase this episode'}
			</button>
		</section>

		{#if snap.chapters.length > 0}
			<section aria-label="Chapters">
				<h2>Chapters</h2>
				<ol>
					{#each snap.chapters as chapter, index (chapter.start)}
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
		{/if}

		{#if snap.notes}
			<section aria-label="Show notes">
				<h2>Show notes</h2>
				<p>{snap.notes}</p>
			</section>
		{/if}

		{#if snap.proposals.length > 0}
			<section aria-label="Editorial notes">
				<h2>Editorial notes</h2>
				<ol>
					{#each snap.proposals as proposal (proposal.id)}
						<li class:covering={proposal.id === snap.coveringId}>
							<p><strong>{proposal.kind}</strong> · words {proposal.startWord} to {proposal.endWord}</p>
							{#if proposal.reason}
								<p>{proposal.reason}</p>
							{/if}
							<p class="dim">{proposal.decision ? `Decision: ${proposal.decision}` : 'Untouched'}</p>
						</li>
					{/each}
				</ol>
			</section>
		{/if}

		{#if snap.moments.length > 0}
			<section aria-label="Quoted moments">
				<h2>Quoted moments</h2>
				<ul>
					{#each snap.moments as moment (`${moment.episodeId}-${moment.offset}`)}
						<li>
							<a
								href={resolve(`/episode/${moment.episodeId}?w=${moment.offset}`)}
								aria-current={moment.offset === snap.momentWord ? 'true' : undefined}
							>
								<span>Word {moment.offset}</span>
								<span>“{moment.quote}”</span>
							</a>
						</li>
					{/each}
				</ul>
			</section>
		{/if}

		{#if snap.words.length > 0}
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
	.state {
		font-size: 0.75rem;
		letter-spacing: 0.1em;
		text-transform: uppercase;
		color: var(--accent);
	}
	h1 {
		font-size: 2rem;
		margin: 0 0 0.5rem;
		color: var(--ink);
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
	section[aria-label='Quoted moments'] ul {
		list-style: none;
		padding: 0;
		margin: 0;
	}
	section[aria-label='Quoted moments'] li a {
		display: block;
		border: 1px solid var(--line);
		border-radius: 0.6rem;
		padding: 0.7rem 0.9rem;
		margin-top: 0.5rem;
		color: var(--muted);
		text-decoration: none;
		line-height: 1.55;
	}
	section[aria-label='Quoted moments'] li a:hover {
		border-color: var(--accent);
	}
	section[aria-label='Quoted moments'] li a span:first-child {
		display: block;
		font-size: 0.75rem;
		letter-spacing: 0.08em;
		color: var(--accent);
		margin-bottom: 0.25rem;
	}
	section[aria-label='Editorial notes'] ol {
		list-style: none;
		padding: 0;
		margin: 0;
	}
	section[aria-label='Editorial notes'] li {
		border: 1px solid var(--line);
		border-radius: 0.6rem;
		padding: 0.7rem 0.9rem;
		margin-top: 0.5rem;
	}
	section[aria-label='Editorial notes'] li.covering {
		border-color: var(--accent);
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
	section[aria-label='Editorial notes'] li,
	section[aria-label='Editorial notes'] li:last-child {
		border-bottom: 1px solid var(--line);
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
	.dim {
		font-size: 0.8rem;
	}
</style>
