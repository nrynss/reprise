<script lang="ts">
	import { resolve } from '$app/paths';
	import { Button } from 'bits-ui';
	import SeasonNav from '$lib/components/SeasonNav.svelte';
	import { pageTitle } from '$lib/shell';
	import { ACCOUNT_PAGE, BACK, CONTINUE, HEADING, START_PATH, SUB } from './google';

	// The start leaves the app for the server route, so it navigates
	// on click instead of linking. Prerender crawls links, and the
	// server route exists only beside the running process.
	function begin() {
		window.location.href = START_PATH;
	}
</script>

<svelte:head>
	<title>{pageTitle('Sign in with Google')}</title>
	<meta
		name="description"
		content="Sign in with Google. The provider confirms it is you, and the diary stays."
	/>
</svelte:head>

<main>
	<p class="eyebrow">Season one</p>
	<h1>{HEADING}</h1>
	<p class="sub">{SUB}</p>
	<nav aria-label="Season">
		<SeasonNav current="none" />
	</nav>

	<div class="choices">
		<Button.Root type="button" onclick={() => begin()}>{CONTINUE}</Button.Root>
		<a class="plain" href={resolve(ACCOUNT_PAGE)}>{BACK}</a>
	</div>
</main>

<style>
	.eyebrow {
		text-transform: uppercase;
		letter-spacing: 0.12em;
		font-size: 0.8rem;
	}
	.sub {
		max-width: 34rem;
	}
	.choices {
		display: grid;
		gap: 1rem;
		max-width: 30rem;
		margin-top: 1rem;
	}
	.plain {
		color: inherit;
	}
</style>
