<script lang="ts">
	import { pageTitle } from '$lib/shell';
</script>

<svelte:head>
	<title>{pageTitle('Privacy')}</title>
	<meta
		name="description"
		content="What Reprise keeps about your episodes, and what erasing deletes."
	/>
</svelte:head>

<main>
	<p>Data · in plain words</p>
	<h1>What Reprise keeps, and for how long</h1>

	<section aria-label="Private by default">
		<h2>Private by default</h2>
		<p>
			Every episode starts private. Only you can see it until you publish.
			A shared link opens only the finished episode and its cover. Your
			words and notes stay in your account.
		</p>
	</section>

	<section aria-label="What erasing removes">
		<h2>What erasing removes</h2>
		<p>
			Erasing an episode deletes its sound, its words, its cover, and its
			notes. It also deletes the copy held for transcription. The copy
			stops being reachable at once. We check that it is gone.
		</p>
		<aside aria-label="One honest caveat">
			<p>
				<strong>One honest caveat:</strong> backups can hold a copy for a time.
			</p>
		</aside>
	</section>

	<section aria-label="Guest recordings">
		<h2>Guest recordings</h2>
		<p>
			A visitor who records gets a full account. A guest's takes expire
			after a set time. Expiry deletes them. It never hides them.
		</p>
	</section>

	<section aria-label="Sessions end explicitly">
		<h2>Sessions end explicitly</h2>
		<p>Recording stops the moment you tap End. Nothing keeps running after that.</p>
	</section>
</main>

<style>
	main {
		max-width: 40rem;
		margin: 0 auto;
		padding: 4rem 1.5rem;
		font-family: system-ui, sans-serif;
	}
	section {
		margin-top: 2rem;
	}
	aside {
		margin-top: 1rem;
		padding: 1rem 1.25rem;
		border: 1px solid #e2d9c8;
		border-radius: 0.75rem;
	}
</style>
