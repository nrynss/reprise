<script lang="ts">
	import { browser } from '$app/environment';
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import DemoBadge from '$lib/components/DemoBadge.svelte';
	import SeasonNav from '$lib/components/SeasonNav.svelte';
	import { pageTitle } from '$lib/shell';
	import {
		emptyLiveThreads,
		episodeNumber,
		fetchThreadsIndex,
		formatClock,
		formatEpisodeNumber,
		listThreads,
		liveQuoteHref,
		LIVE_THREADS_EMPTY_NOTICE,
		LIVE_THREADS_FAILED_NOTICE,
		LIVE_THREADS_LOADING_NOTICE,
		LIVE_THREADS_NOTICE,
		queryValue,
		quoteHref,
		runSeasonGates
	} from './threads';

	let ready = $state(false);
	let notice = $state(LIVE_THREADS_LOADING_NOTICE);
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
			notice = LIVE_THREADS_NOTICE;
		} else {
			live = true;
			try {
				const index = await fetchThreadsIndex(window.fetch);
				liveNames = index.names;
				liveTopics = index.topics;
				ready = true;
				notice =
					index.names.length === 0 && index.topics.length === 0
						? LIVE_THREADS_EMPTY_NOTICE
						: LIVE_THREADS_NOTICE;
			} catch {
				ready = true;
				failed = true;
				notice = LIVE_THREADS_FAILED_NOTICE;
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
		content="People and topics that keep coming up, and the moments you mentioned them."
	/>
</svelte:head>

<main>
	<p class="eyebrow">The thread across episodes</p>
	<h1>What keeps coming up</h1>
	<p class="sub">
		People and topics that keep coming up, and the moments you mentioned them.
	</p>
	{#if !live}
		<DemoBadge />
	{/if}
	{#if notice}
		<p role="status">{notice}</p>
	{/if}
	{#if failed}
		<button onclick={() => void load()}>Retry the threads</button>
	{/if}
	<nav aria-label="Season">
		<SeasonNav current="threads" fixture={!live} />
	</nav>

	{#if ready && !failed}
		{#if !live}
			<section class="section" aria-label="Open commitments">
				<h2>Open commitments</h2>
				{#each commitments as item (item.id)}
					<article class="card" aria-label={item.name}>
						<h3>{item.name}</h3>
						{#if item.who}<p class="who">{item.who}</p>{/if}
						<p class="facts">{item.count}{item.opened ? ` · since ${item.opened}` : ''}</p>
						{#if item.status}
							<p class="state">{item.status === 'open' ? 'Open' : 'Resolved'}</p>
						{/if}
						<ul class="quotes">
							{#each item.quotes as quote (`${quote.episode}-${quote.offset}`)}
								<li>
									<a href={resolve(quoteHref(quote.episode, quote.offset))}>
										<span class="when"
											>{formatEpisodeNumber(episodeNumber(quote.episode))} · {formatClock(
												quote.offset
											)}</span
										>
										<span class="words">“{quote.text}”</span>
										<span class="open">Play from this quote</span>
									</a>
								</li>
							{/each}
						</ul>
					</article>
				{/each}
			</section>

			<section class="section" aria-label="People who recur">
				<h2>People who recur</h2>
				{#each people as item (item.id)}
					<article class="card" aria-label={item.name}>
						<h3>{item.name}</h3>
						{#if item.who}<p class="who">{item.who}</p>{/if}
						<p class="facts">{item.count}</p>
						<ul class="quotes">
							{#each item.quotes as quote (`${quote.episode}-${quote.offset}`)}
								<li>
									<a href={resolve(quoteHref(quote.episode, quote.offset))}>
										<span class="when"
											>{formatEpisodeNumber(episodeNumber(quote.episode))} · {formatClock(
												quote.offset
											)}</span
										>
										<span class="words">“{quote.text}”</span>
										<span class="open">Play from this quote</span>
									</a>
								</li>
							{/each}
						</ul>
					</article>
				{/each}
			</section>

			<section class="section" aria-label="Circled topics">
				<h2>Circled topics</h2>
				{#each topics as item (item.id)}
					<article class="card" aria-label={item.name}>
						<h3>{item.name}</h3>
						<p class="facts">{item.count}</p>
						<ul class="quotes">
							{#each item.quotes as quote (`${quote.episode}-${quote.offset}`)}
								<li>
									<a href={resolve(quoteHref(quote.episode, quote.offset))}>
										<span class="when"
											>{formatEpisodeNumber(episodeNumber(quote.episode))} · {formatClock(
												quote.offset
											)}</span
										>
										<span class="words">“{quote.text}”</span>
										<span class="open">Play from this quote</span>
									</a>
								</li>
							{/each}
						</ul>
					</article>
				{/each}
			</section>
		{:else}
			<section class="section" aria-label="People who recur">
				<h2>People who recur</h2>
				{#each liveNames as item (item.key)}
					<article class="card" aria-label={item.display}>
						<h3>{item.display}</h3>
						<p class="facts">{item.mentionCount} mentions · {item.episodeCount} episodes</p>
						<ul class="quotes">
							{#each item.episodes as hit (`${hit.episodeId}-${hit.offset}`)}
								<li>
									<a href={resolve(liveQuoteHref(hit.episodeId, hit.offset))}>
										<span class="when">{formatEpisodeNumber(hit.number)} · word {hit.offset}</span>
										<span class="words">“{hit.quote}”</span>
										<span class="open">Open at this quote</span>
									</a>
								</li>
							{/each}
						</ul>
					</article>
				{/each}
			</section>

			<section class="section" aria-label="Circled topics">
				<h2>Circled topics</h2>
				{#each liveTopics as item (item.key)}
					<article class="card" aria-label={item.display}>
						<h3>{item.display}</h3>
						<p class="facts">{item.mentionCount} mentions · {item.episodeCount} episodes</p>
						<ul class="quotes">
							{#each item.episodes as hit (`${hit.episodeId}-${hit.offset}`)}
								<li>
									<a href={resolve(liveQuoteHref(hit.episodeId, hit.offset))}>
										<span class="when">{formatEpisodeNumber(hit.number)} · word {hit.offset}</span>
										<span class="words">“{hit.quote}”</span>
										<span class="open">Open at this quote</span>
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
	.section .card {
		margin-bottom: 1rem;
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
	.state {
		font-size: 0.8rem;
		letter-spacing: 0.08em;
		text-transform: uppercase;
		color: var(--accent);
		margin: 0 0 0.75rem;
	}
	.quotes {
		list-style: none;
		padding: 0;
		margin: 1rem 0 0;
		border-top: 1px solid var(--line);
	}
	.quotes li {
		padding: 0.75rem 0;
		border-bottom: 1px solid var(--line);
	}
	.quotes a {
		display: block;
		color: var(--ink);
		text-decoration: none;
		line-height: 1.55;
	}
	.quotes a:hover .words {
		text-decoration: underline;
	}
	.quotes .when {
		display: block;
		font-size: 0.75rem;
		letter-spacing: 0.08em;
		color: var(--accent);
		margin-bottom: 0.25rem;
	}
	.open {
		display: block;
		font-size: 0.8rem;
		color: var(--accent);
		margin-top: 0.35rem;
	}
</style>
