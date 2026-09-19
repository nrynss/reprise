<script lang="ts">
	import { pageTitle } from '$lib/shell';
</script>

<svelte:head>
	<title>{pageTitle('Privacy')}</title>
	<meta
		name="description"
		content="What Reprise keeps, what erasing deletes, and what the speech provider keeps on its side."
	/>
</svelte:head>

<main>
	<p>Data · in plain words</p>
	<h1>What Reprise keeps, and for how long</h1>

	<section aria-label="Private by default">
		<h2>Private by default</h2>
		<p>
			Every episode starts private. Publishing is a separate act per episode,
			and a share link opens only the finished render and its cover. Stems,
			transcripts, and threads never leave your account.
		</p>
	</section>

	<section aria-label="What erasing removes">
		<h2>What erasing removes</h2>
		<p>
			Erasing an episode deletes every row, both stems, the render, and the
			cover from this studio. It also deletes the provider copy of the
			session and every batch transcript left behind it.
		</p>
		<aside aria-label="Provider deletion caveat">
			<p>
				<strong>One honest caveat:</strong> the speech provider deletes softly
				on its side. The copy stops being reachable, and this studio
				re-verifies the deletion. The provider keeps its own backups under
				its own retention policy.
			</p>
		</aside>
	</section>

	<section aria-label="Guest recordings">
		<h2>Guest recordings</h2>
		<p>
			A visitor who records gets a real account with one code path, not a
			lesser copy of one. Guest data expires on a timer the studio sets,
			and expiry deletes rather than hides.
		</p>
	</section>

	<section aria-label="Sessions end explicitly">
		<h2>Sessions end explicitly</h2>
		<p>
			Streaming bills on connected time, so the session ends the moment you
			tap End. A token cap stops anything the browser leaves open. Nothing
			runs open.
		</p>
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
