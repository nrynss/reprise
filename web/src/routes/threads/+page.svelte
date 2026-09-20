<script lang="ts">
	import { browser } from '$app/environment';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { pageTitle } from '$lib/shell';
	import {
		emptyLiveThreads,
		episodeNumber,
		fetchThreadsIndex,
		formatClock,
		formatEpisodeNumber,
		listThreads,
		liveQuoteHref,
		queryValue,
		quoteHref,
		runSeasonGates
	} from './threads';

	let ready = $state(false);
	let notice = $state('Loading the threads.');
	let failed = $state(false);
	let live = $state(true);
	const blank = listThreads(false);
	const emptyIndex = emptyLiveThreads();
	let commitments = $state(blank.commitments);
	let people = $state(blank.people);
	let topics = $state(blank.topics);
	let liveNames = $state(emptyIndex.names);
	let liveTopics = $state(emptyIndex.topics);
	let gateResult = $state('');
	let search = $state('');

	async function load() {
		ready = false;
		failed = false;
		if (queryValue(search, 'fixture') === '1') {
			live = false;
			const fifthReady = queryValue(search, 'after5') === '1';
			const threads = listThreads(fifthReady);
			commitments = threads.commitments;
			people = threads.people;
			topics = threads.topics;
			ready = true;
			notice = fifthReady
				? 'Scripted threads with episode five folded in. No backend needed.'
				: 'Scripted threads. No backend needed.';
		} else {
			live = true;
			try {
				const index = await fetchThreadsIndex(window.fetch);
				liveNames = index.names;
				liveTopics = index.topics;
				ready = true;
				notice =
					index.names.length === 0 && index.topics.length === 0
						? 'Nothing threads yet. Two episodes must name someone before a row forms.'
						: 'Live threads from stored mentions. Quotes and links, nothing else.';
			} catch {
				ready = true;
				failed = true;
				notice = 'The thread index refused, so nothing renders. Retry the load.';
			}
		}
		if (queryValue(search, 'gate') === '1') {
			const main = document.querySelector('main');
			if (main) {
				gateResult = await runSeasonGates(main);
			}
		}
	}

	$effect(() => {
		if (!browser) return;
		search = page.url.search;
		void load();
	});
</script>

<svelte:head>
	<title>{pageTitle('Threads')}</title>
	<meta
		name="description"
		content="The thread across episodes: people, promises, and circling topics. Quotes and links, nothing else."
	/>
</svelte:head>

<main>
	<p class="eyebrow">The thread across episodes</p>
	<h1>What keeps coming up</h1>
	<p class="sub">
		People, promises, and circling topics. Each one links to the moment it was
		said. No scores, no gauges. Quotes and links, nothing else.
	</p>
	<p role="status">{notice}</p>
	{#if failed}
		<button onclick={() => void load()}>Retry the threads</button>
	{/if}
	<nav aria-label="Season">
		{#if live}
			<a href={resolve('/')}>Gallery</a>
			<a href={resolve('/threads')} aria-current="page">Threads</a>
		{:else}
			<a href={resolve('/?fixture=1')}>Gallery</a>
			<a href={resolve('/threads?fixture=1')} aria-current="page">Threads</a>
		{/if}
	</nav>

	{#if ready && !failed}
		{#if !live}
			<section aria-label="Open commitments">
				<h2>Open commitments</h2>
				{#each commitments as item (item.id)}
					<article aria-label={item.name}>
						<h3>{item.name}</h3>
						{#if item.who}<p class="who">{item.who}</p>{/if}
						<p class="facts">{item.count}{item.opened ? ` · since ${item.opened}` : ''}</p>
						{#if item.status}
							<p class="status">{item.status === 'open' ? 'Open' : 'Resolved'}</p>
						{/if}
						<ul>
							{#each item.quotes as quote (`${quote.episode}-${quote.offset}`)}
								<li>
									<a href={resolve(quoteHref(quote.episode, quote.offset))}>
										<span
											>{formatEpisodeNumber(episodeNumber(quote.episode))} · {formatClock(
												quote.offset
											)}</span
										>
										<span>“{quote.text}”</span>
										<span class="listen">Play from this quote</span>
									</a>
								</li>
							{/each}
						</ul>
					</article>
				{/each}
			</section>

			<section aria-label="People who recur">
				<h2>People who recur</h2>
				{#each people as item (item.id)}
					<article aria-label={item.name}>
						<h3>{item.name}</h3>
						{#if item.who}<p class="who">{item.who}</p>{/if}
						<p class="facts">{item.count}</p>
						<ul>
							{#each item.quotes as quote (`${quote.episode}-${quote.offset}`)}
								<li>
									<a href={resolve(quoteHref(quote.episode, quote.offset))}>
										<span
											>{formatEpisodeNumber(episodeNumber(quote.episode))} · {formatClock(
												quote.offset
											)}</span
										>
										<span>“{quote.text}”</span>
										<span class="listen">Play from this quote</span>
									</a>
								</li>
							{/each}
						</ul>
					</article>
				{/each}
			</section>

			<section aria-label="Circled topics">
				<h2>Circled topics</h2>
				{#each topics as item (item.id)}
					<article aria-label={item.name}>
						<h3>{item.name}</h3>
						<p class="facts">{item.count}</p>
						<ul>
							{#each item.quotes as quote (`${quote.episode}-${quote.offset}`)}
								<li>
									<a href={resolve(quoteHref(quote.episode, quote.offset))}>
										<span
											>{formatEpisodeNumber(episodeNumber(quote.episode))} · {formatClock(
												quote.offset
											)}</span
										>
										<span>“{quote.text}”</span>
										<span class="listen">Play from this quote</span>
									</a>
								</li>
							{/each}
						</ul>
					</article>
				{/each}
			</section>
		{:else}
			<section aria-label="People who recur">
				<h2>People who recur</h2>
				{#each liveNames as item (item.key)}
					<article aria-label={item.display}>
						<h3>{item.display}</h3>
						<p class="facts">{item.mentionCount} mentions · {item.episodeCount} episodes</p>
						<ul>
							{#each item.episodes as hit (`${hit.episodeId}-${hit.offset}`)}
								<li>
									<a href={resolve(liveQuoteHref(hit.episodeId, hit.offset))}>
										<span>{formatEpisodeNumber(hit.number)} · word {hit.offset}</span>
										<span>“{hit.quote}”</span>
										<span class="listen">Open at this quote</span>
									</a>
								</li>
							{/each}
						</ul>
					</article>
				{/each}
			</section>

			<section aria-label="Circled topics">
				<h2>Circled topics</h2>
				{#each liveTopics as item (item.key)}
					<article aria-label={item.display}>
						<h3>{item.display}</h3>
						<p class="facts">{item.mentionCount} mentions · {item.episodeCount} episodes</p>
						<ul>
							{#each item.episodes as hit (`${hit.episodeId}-${hit.offset}`)}
								<li>
									<a href={resolve(liveQuoteHref(hit.episodeId, hit.offset))}>
										<span>{formatEpisodeNumber(hit.number)} · word {hit.offset}</span>
										<span>“{hit.quote}”</span>
										<span class="listen">Open at this quote</span>
									</a>
								</li>
							{/each}
						</ul>
					</article>
				{/each}
			</section>
		{/if}
	{/if}

	{#if gateResult}
		<p id="gate-status" role="status">{gateResult}</p>
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
	.sub {
		color: var(--muted);
		line-height: 1.6;
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
	nav {
		display: flex;
		gap: 1rem;
		margin: 1.5rem 0;
	}
	nav a {
		color: var(--accent);
	}
	h2 {
		font-size: 1.25rem;
		margin: 2.5rem 0 1rem;
		color: var(--ink);
	}
	article {
		background: var(--raised);
		border: 1px solid var(--line);
		border-radius: 0.75rem;
		padding: 1.25rem 1.5rem;
		margin-bottom: 1rem;
	}
	article h3 {
		margin: 0 0 0.25rem;
		font-size: 1.35rem;
		color: var(--ink);
	}
	.who {
		color: var(--muted);
		font-size: 0.9rem;
		margin: 0 0 0.25rem;
	}
	.facts {
		font-size: 0.8rem;
		color: var(--muted);
		margin: 0 0 0.75rem;
	}
	.status {
		font-size: 0.8rem;
		letter-spacing: 0.08em;
		text-transform: uppercase;
		color: var(--accent);
		margin: 0 0 0.75rem;
	}
	ul {
		list-style: none;
		padding: 0;
		margin: 0;
	}
	li a {
		display: block;
		border: 1px solid var(--line);
		border-radius: 0.6rem;
		padding: 0.7rem 0.9rem;
		margin-top: 0.5rem;
		color: var(--muted);
		text-decoration: none;
		line-height: 1.55;
	}
	li a:hover {
		border-color: var(--accent);
	}
	li a span:first-child {
		display: block;
		font-size: 0.75rem;
		letter-spacing: 0.08em;
		color: var(--accent);
		margin-bottom: 0.25rem;
	}
	.listen {
		display: block;
		font-size: 0.8rem;
		color: var(--accent);
		margin-top: 0.35rem;
	}
</style>
