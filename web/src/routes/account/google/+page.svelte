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
	<h1>{HEADING}</h1>
	<p class="sub">{SUB}</p>
	<nav aria-label="Season">
		<SeasonNav current="none" />
	</nav>

	<div class="actions">
		<Button.Root type="button" class="button" onclick={() => begin()}>{CONTINUE}</Button.Root>
		<a class="button quiet" href={resolve(ACCOUNT_PAGE)}>{BACK}</a>
	</div>
</main>
