<script lang="ts">
	import { pageTitle } from '$lib/shell';
	import {
		emptyAdminSnapshot,
		fetchSnapshot,
		formatCents,
		formatMinutes,
		formatSpend,
		guestSpendNotice,
		OWNER_REQUIRED,
		pausedLabel,
		setPaused
	} from './limits';

	let phase = $state('loading');
	let snapshot = $state(emptyAdminSnapshot);
	let failure = $state('');
	let ownerQuery = $state('');
	let flipping = $state(false);

	async function load(owner = '') {
		phase = 'loading';
		failure = '';
		try {
			snapshot = await fetchSnapshot(fetch, owner || undefined);
			phase = 'ready';
		} catch (error) {
			if (error instanceof Error && error.message === OWNER_REQUIRED) {
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
		<p role="status">Reading the caps and today&apos;s spend…</p>
	{/if}

	{#if phase === 'denied'}
		<section aria-label="Owner sign-in">
			<h2>Owner sign-in needed</h2>
			<p>
				This page flips the session switch and reads today&apos;s spend. It opens
				once the owner login lands. Guests never reach these controls.
			</p>
		</section>
	{/if}

	{#if phase === 'failed'}
		<p role="alert">The admin page failed: {failure}</p>
		<button onclick={() => void load(ownerQuery || undefined)}>Retry</button>
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
			<button disabled={flipping} onclick={() => void flip()}>
				{flipping ? 'Flipping…' : snapshot.sessions_paused ? 'Resume sessions' : 'Pause sessions'}
			</button>
		</section>

		<section aria-label="Today's spend">
			<h2>Today&apos;s spend</h2>
			<p>
				Spent {formatSpend(snapshot.global.spent_nd)} of {formatSpend(
					snapshot.global.ceiling_nd
				)} today. The ledger carries no history, so this page shows today only.
			</p>
		</section>

		<section aria-label="Guest caps">
			<h2>Guest caps</h2>
			<ul>
				<li>Sessions per guest: {snapshot.caps.guest_max_sessions}</li>
				<li>Session length: {formatMinutes(snapshot.caps.session_max_seconds)}</li>
				<li>Daily ceiling: {formatCents(snapshot.caps.daily_spend_cents)}</li>
			</ul>
		</section>

		<section aria-label="Guest lookup">
			<h2>Guest lookup</h2>
			<form
				onsubmit={(event) => {
					event.preventDefault();
					void load(ownerQuery || undefined);
				}}
			>
				<label>
					Owner id
					<input bind:value={ownerQuery} name="owner" autocomplete="off" />
				</label>
				<button type="submit">Look up</button>
			</form>
			{#if guestSpendNotice(snapshot, ownerQuery)}
				<p role="status">{guestSpendNotice(snapshot, ownerQuery)}</p>
			{/if}
			<p>Past the cap, a guest sees the guest limit reached notice.</p>
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
