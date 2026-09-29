<script lang="ts">
	import { pageTitle } from '$lib/shell';
	import {
		AdminRefusal,
		ADMIN_LIMITS_HEADING,
		ADMIN_LONGEST_SESSION_LABEL,
		ADMIN_PAUSE_FINISHES,
		ADMIN_SESSIONS_PER_GUEST_LABEL,
		ADMIN_TOTAL_SPEND_HEADING,
		emptyAdminSnapshot,
		fetchSnapshot,
		formatMinutes,
		globalSpendLine,
		OPERATOR_REQUIRED,
		OWNER_REQUIRED,
		pausedLabel,
		setPaused,
		spendingCeilingLine
	} from './limits';

	let phase = $state('loading');
	let snapshot = $state(emptyAdminSnapshot);
	let failure = $state('');
	let denial = $state<'signin' | 'forbidden' | ''>('');
	let flipping = $state(false);

	async function load() {
		phase = 'loading';
		failure = '';
		denial = '';
		try {
			snapshot = await fetchSnapshot(fetch);
			phase = 'ready';
		} catch (error) {
			if (error instanceof AdminRefusal && error.code === OPERATOR_REQUIRED) {
				denial = error.status === 403 ? 'forbidden' : 'signin';
				phase = 'denied';
				return;
			}
			if (error instanceof Error && error.message === OWNER_REQUIRED) {
				denial = 'signin';
				phase = 'denied';
				return;
			}
			failure = error instanceof Error ? error.message : 'the admin page could not load';
			phase = 'failed';
		}
	}

	async function flip() {
		const next = !snapshot.sessions_paused;
		flipping = true;
		try {
			snapshot.sessions_paused = await setPaused(fetch, next);
		} catch (error) {
			if (error instanceof AdminRefusal && error.code === OPERATOR_REQUIRED) {
				denial = error.status === 403 ? 'forbidden' : 'signin';
				phase = 'denied';
				return;
			}
			failure = error instanceof Error ? error.message : 'the switch could not flip';
			phase = 'failed';
		} finally {
			flipping = false;
		}
	}

	$effect(() => {
		void load();
	});
</script>

<svelte:head>
	<title>{pageTitle('Admin')}</title>
</svelte:head>

<main>
	<h1>Admin</h1>

	{#if phase === 'loading'}
		<p role="status">Reading the limits and the spend…</p>
	{/if}

	{#if phase === 'denied'}
		{#if denial === 'forbidden'}
			<section aria-label="Operator access">
				<h2>Operator access needed</h2>
				<p>
					This page flips the session switch and reads the spend. This address
					is signed in, but it is not on the operator list.
				</p>
			</section>
		{:else}
			<section aria-label="Operator sign-in">
				<h2>Sign in needed</h2>
				<p>
					This page flips the session switch and reads the spend. Sign in
					with an operator address to open it.
				</p>
			</section>
		{/if}
	{/if}

	{#if phase === 'failed'}
		<p role="alert">The admin page failed: {failure}</p>
		<button onclick={() => void load()}>Retry</button>
	{/if}

	{#if phase === 'ready'}
		<section aria-label="Session switch">
			<h2>{pausedLabel(snapshot)}</h2>
			<p>
				{#if snapshot.sessions_paused}
					New sessions refuse with a paused notice until the switch flips back.
				{:else}
					Guests under the cap start sessions as usual.
				{/if}
			</p>
			<p>{ADMIN_PAUSE_FINISHES}</p>
			<button disabled={flipping} onclick={() => void flip()}>
				{flipping ? 'Flipping…' : snapshot.sessions_paused ? 'Resume sessions' : 'Pause sessions'}
			</button>
		</section>

		<section aria-label={ADMIN_TOTAL_SPEND_HEADING}>
			<h2>{ADMIN_TOTAL_SPEND_HEADING}</h2>
			<p>{globalSpendLine(snapshot)}</p>
		</section>

		<section aria-label={ADMIN_LIMITS_HEADING}>
			<h2>{ADMIN_LIMITS_HEADING}</h2>
			<ul>
				<li>{ADMIN_SESSIONS_PER_GUEST_LABEL}: {snapshot.caps.guest_max_sessions}</li>
				<li>{ADMIN_LONGEST_SESSION_LABEL}: {formatMinutes(snapshot.caps.session_max_seconds)}</li>
				<li>{spendingCeilingLine(snapshot.caps)}</li>
			</ul>
		</section>
	{/if}
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
</style>
