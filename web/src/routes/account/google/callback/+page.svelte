<script lang="ts">
	import { browser } from '$app/environment';
	import { resolve } from '$app/paths';
	import { Button } from 'bits-ui';
	import SeasonNav from '$lib/components/SeasonNav.svelte';
	import { pageTitle } from '$lib/shell';
	import {
		ACCOUNT_PAGE,
		CONFLICT_BODY,
		CONFLICT_HEADING,
		DONE,
		FAILED,
		GOOGLE_PAGE,
		KEEP,
		SWITCH,
		TRY_AGAIN,
		VIEW_ACCOUNT,
		callbackState,
		outcome,
		switchHref
	} from '../google';

	// The page renders the flag the sign-in route landed on. Prerender
	// carries no query, so the flag starts at the beginning and the
	// browser fills it in after hydration.
	let flag = $state<'done' | 'conflict' | 'failed' | 'start'>('start');
	let flowState = $state('');

	// The switch retry hits the server route, so it navigates on
	// click instead of linking. Prerender crawls links, and the
	// server route exists only beside the running process.
	function move() {
		if (!browser || flowState === '') return;
		window.location.href = switchHref(flowState);
	}

	$effect(() => {
		if (!browser) return;
		const search = window.location.search;
		flag = outcome(search);
		flowState = callbackState(search) ?? '';
	});
</script>

<svelte:head>
	<title>{pageTitle('Google sign-in')}</title>
	<meta
		name="description"
		content="The Google sign-in answer. Done signs in, conflict picks a diary."
	/>
</svelte:head>

<main>
	<p class="eyebrow">Season one</p>
	<h1>Google sign-in</h1>
	<nav aria-label="Season">
		<SeasonNav current="none" />
	</nav>

	{#if flag === 'done'}
		<p role="status">{DONE}</p>
		<a href={resolve(ACCOUNT_PAGE)}>{VIEW_ACCOUNT}</a>
	{:else if flag === 'conflict'}
		<section aria-label="Pick a diary">
			<h2>{CONFLICT_HEADING}</h2>
			<p>{CONFLICT_BODY}</p>
			<div class="choices">
				<!-- Keeping leaves the device alone, so it links to the account screen. -->
				<a href={resolve(ACCOUNT_PAGE)}>{KEEP}</a>
				{#if flowState !== ''}
					<Button.Root type="button" onclick={() => move()}>{SWITCH}</Button.Root>
				{/if}
			</div>
		</section>
	{:else if flag === 'failed'}
		<p role="alert">{FAILED}</p>
		<a href={resolve(GOOGLE_PAGE)}>{TRY_AGAIN}</a>
	{:else}
		<a href={resolve(GOOGLE_PAGE)}>{TRY_AGAIN}</a>
	{/if}
</main>

<style>
	.eyebrow {
		text-transform: uppercase;
		letter-spacing: 0.12em;
		font-size: 0.8rem;
	}
	.choices {
		display: grid;
		gap: 1rem;
		max-width: 30rem;
		margin-top: 1rem;
	}
</style>
