<script lang="ts">
	import { resolve } from '$app/paths';
	import { Button, Label } from 'bits-ui';
	import { ApiError } from '@nrynss/chaaya/api';
	import SeasonNav from '$lib/components/SeasonNav.svelte';
	import { pageTitle } from '$lib/shell';
	import {
		CHECK_CODE,
		CODE_LABEL,
		ENTER_ADDRESS,
		ENTER_CODE,
		EMAIL_HELP,
		EMAIL_LABEL,
		INVALID_CODE,
		INVALID_REQUEST,
		REQUEST_FAILED,
		RESEND_CODE,
		SEND_CODE,
		SEND_LIMITED,
		codeSentNotice,
		forgetSignedInEmail,
		normalizeEmail,
		requestCode,
		retryWaitNotice
	} from '../account';
	import {
		DELETE_BACK,
		DELETE_BODY,
		DELETE_CANCEL,
		DELETE_CODE_HELP,
		DELETE_CONFIRM,
		DELETE_DONE,
		DELETE_FAILED,
		DELETE_HEADING,
		DELETE_SUB,
		deleteAccount
	} from './delete';

	// The delete screen moves through one phase at a time. The address
	// step asks for a code, the code step reads it, the confirm step
	// says what goes, and the done step starts a fresh diary.
	let phase = $state<'email' | 'code' | 'confirm' | 'done'>('email');
	let email = $state('');
	let code = $state('');
	let notice = $state('');
	let problem = $state('');
	let busy = $state(false);

	// send asks for a code at the typed address. A capped answer names
	// the wait, and any other refusal names the next step.
	async function send() {
		const address = normalizeEmail(email);
		if (address.length === 0) {
			problem = ENTER_ADDRESS;
			return;
		}
		busy = true;
		problem = '';
		try {
			await requestCode(fetch, address);
			phase = 'code';
			notice = codeSentNotice(address);
		} catch (error) {
			if (error instanceof ApiError && error.code === SEND_LIMITED) {
				problem = retryWaitNotice(error.retryAfterSeconds ?? 60);
			} else if (error instanceof ApiError && error.code === INVALID_REQUEST) {
				problem = ENTER_ADDRESS;
			} else {
				problem = REQUEST_FAILED;
			}
		} finally {
			busy = false;
		}
	}

	// read moves from the code step to the confirm step. An empty code
	// field names the next step instead of asking the server.
	function read() {
		if (code.trim().length === 0) {
			problem = ENTER_CODE;
			return;
		}
		problem = '';
		notice = '';
		phase = 'confirm';
	}

	// remove deletes the account behind the typed code. A refused code
	// returns to the code step, so a fresh code can take its place.
	async function remove() {
		busy = true;
		problem = '';
		try {
			await deleteAccount(fetch, normalizeEmail(email), code.trim());
			forgetSignedInEmail();
			email = '';
			code = '';
			notice = '';
			phase = 'done';
		} catch (error) {
			if (error instanceof ApiError && error.code === INVALID_CODE) {
				phase = 'code';
				problem = DELETE_CODE_HELP;
			} else {
				problem = DELETE_FAILED;
			}
		} finally {
			busy = false;
		}
	}

	// keep returns to the code step with the typed code still in place.
	function keep() {
		problem = '';
		phase = 'code';
	}
</script>

<svelte:head>
	<title>{pageTitle('Delete account')}</title>
	<meta
		name="description"
		content="Delete your account. Every take, session, and address goes."
	/>
</svelte:head>

<main>
	<p class="eyebrow">Season one</p>
	<h1>{DELETE_HEADING}</h1>
	<p class="sub">{DELETE_SUB}</p>
	<nav aria-label="Season">
		<SeasonNav current="none" />
	</nav>

	{#if notice}
		<p role="status">{notice}</p>
	{/if}
	{#if problem}
		<p role="alert">{problem}</p>
	{/if}

	{#if phase === 'email'}
		<form
			onsubmit={(event) => {
				event.preventDefault();
				void send();
			}}
		>
			<div class="field">
				<Label.Root for="delete-email">{EMAIL_LABEL}</Label.Root>
				<input
					id="delete-email"
					type="email"
					autocomplete="email"
					bind:value={email}
					disabled={busy}
					required
				/>
				<p class="help">{EMAIL_HELP}</p>
			</div>
			<Button.Root type="submit" disabled={busy}>{SEND_CODE}</Button.Root>
		</form>
	{/if}

	{#if phase === 'code'}
		<form
			onsubmit={(event) => {
				event.preventDefault();
				read();
			}}
		>
			<div class="field">
				<Label.Root for="delete-code">{CODE_LABEL}</Label.Root>
				<input
					id="delete-code"
					inputmode="numeric"
					autocomplete="one-time-code"
					maxlength={6}
					bind:value={code}
					disabled={busy}
					required
				/>
				<p class="help">{DELETE_CODE_HELP}</p>
			</div>
			<Button.Root type="submit" disabled={busy}>{CHECK_CODE}</Button.Root>
			<div class="resend">
				<Button.Root type="button" disabled={busy} onclick={() => void send()}>
					{RESEND_CODE}
				</Button.Root>
			</div>
		</form>
	{/if}

	{#if phase === 'confirm'}
		<section aria-label="Confirm deletion">
			<p>{DELETE_BODY}</p>
			<div class="choices">
				<Button.Root type="button" disabled={busy} onclick={() => void remove()}>
					{DELETE_CONFIRM}
				</Button.Root>
				<Button.Root type="button" disabled={busy} onclick={() => keep()}>
					{DELETE_CANCEL}
				</Button.Root>
			</div>
		</section>
	{/if}

	{#if phase === 'done'}
		<section aria-label="Deleted">
			<p role="status">{DELETE_DONE}</p>
			<div class="choices">
				<a href={resolve('/account')}>{DELETE_BACK}</a>
			</div>
		</section>
	{/if}
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
	.field {
		display: grid;
		gap: 0.4rem;
		max-width: 22rem;
		margin: 1rem 0;
	}
	.help {
		font-size: 0.9rem;
		opacity: 0.85;
	}
	input {
		padding: 0.55rem 0.8rem;
		border-radius: 8px;
		border: 1px solid currentColor;
		font-size: 1rem;
	}
	.resend {
		margin-top: 0.75rem;
	}
	.choices {
		display: grid;
		gap: 1rem;
		max-width: 30rem;
		margin-top: 1rem;
	}
</style>
