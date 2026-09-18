<script lang="ts">
	import { appName, appTagline } from '$lib/shell';
</script>

<svelte:head>
	<title>{appName}</title>
	<meta name="description" content={appTagline} />
</svelte:head>

<main>
	<h1>{appName}</h1>
	<p>{appTagline}</p>
</main>

<style>
	main {
		max-width: 40rem;
		margin: 0 auto;
		padding: 4rem 1.5rem;
		font-family: system-ui, sans-serif;
	}
	h1 {
		font-size: 2rem;
	}
</style>
