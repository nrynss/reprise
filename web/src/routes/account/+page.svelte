<script lang="ts">
	import { browser } from '$app/environment';
	import { Button, Label } from 'bits-ui';
	import { ApiError } from '@nrynss/chaaya/api';
	import SeasonNav from '$lib/components/SeasonNav.svelte';
	import { pageTitle } from '$lib/shell';
	import {
		ACCOUNT_HEADING,
		ACCOUNT_SUB,
		CHECK_CODE,
		CODE_LABEL,
		CONFLICT_BODY,
		CONFLICT_HEADING,
		DEFAULT_RESEND_WAIT,
		DELETE_ACCOUNT,
		DELETE_SOON,
		EMAIL_HELP,
		EMAIL_LABEL,
		ENTER_ADDRESS,
		ENTER_CODE,
		INVALID_CODE,
		INVALID_REQUEST,
		KEEP_DIARY,
		KEEP_HELP,
		KEPT_DIARY,
		REQUEST_FAILED,
		RESEND_CODE,
		SEND_CODE,
		SEND_LIMITED,
		SIGN_OUT,
		SIGN_OUT_FAILED,
		SIGNED_OUT,
		SWITCH_ACCOUNT,
		SWITCH_HELP,
		WRONG_CODE,
		codeSentNotice,
		forgetSignedInEmail,
		normalizeEmail,
		readSignedInEmail,
		rememberSignedInEmail,
		requestCode,
		retryWaitNotice,
		signedInNotice,
		signOut,
		verifyCode
	} from './account';

	// The account screens move through one phase at a time. The address
	// step asks for a code, the code step checks it, the conflict step
	// picks a diary, and the signed in step shows the address.
	let phase = $state<'email' | 'code' | 'conflict' | 'signedin'>('email');
	let email = $state('');
	let code = $state('');
	let notice = $state('');
	let problem = $state('');
	let busy = $state(false);
	let resendWait = $state(0);
	let resendTimer = $state(0);

	// stopResendTimer drops a pending resend countdown, if one runs.
	// Zero means no countdown, so clearing it never fires.
	function stopResendTimer() {
		if (resendTimer !== 0) {
			clearTimeout(resendTimer);
			resendTimer = 0;
		}
	}

	// armResendCooldown disables the resend control for some seconds. The
	// page honours the capped answer, so a fast finger never spends more.
	// The default means no wait, so a bare call never locks the control.
	function armResendCooldown(seconds = 0) {
		stopResendTimer();
		resendWait = seconds;
		if (!browser || seconds <= 0) return;
		resendTimer = setTimeout(() => {
			resendWait = 0;
			resendTimer = 0;
		}, seconds * 1000);
	}

	// send asks for a code at the typed address. A capped answer starts
	// the resend countdown, and any other refusal names the next step.
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
				const wait = error.retryAfterSeconds ?? DEFAULT_RESEND_WAIT;
				armResendCooldown(wait);
				problem = retryWaitNotice(wait);
			} else if (error instanceof ApiError && error.code === INVALID_REQUEST) {
				problem = ENTER_ADDRESS;
			} else {
				problem = REQUEST_FAILED;
			}
		} finally {
			busy = false;
		}
	}

	// check verifies the typed code. A conflict opens the diary choice,
	// and a success stores the address beside the fresh session cookie.
	// The choice stays blank except on the repeat check past a conflict.
	async function check(choice = '') {
		if (code.trim().length === 0) {
			problem = ENTER_CODE;
			return;
		}
		busy = true;
		problem = '';
		try {
			const result = await verifyCode(
				fetch,
				normalizeEmail(email),
				code.trim(),
				choice === 'switch' ? 'switch' : undefined
			);
			if (result === 'conflict') {
				phase = 'conflict';
				return;
			}
			rememberSignedInEmail(email);
			email = normalizeEmail(email);
			code = '';
			notice = '';
			phase = 'signedin';
		} catch (error) {
			problem = error instanceof ApiError && error.code === INVALID_CODE ? WRONG_CODE : REQUEST_FAILED;
		} finally {
			busy = false;
		}
	}

	// keep stays a guest on this device. Nothing moves, and the address
	// step returns with the typed address still in place.
	function keep() {
		code = '';
		notice = KEPT_DIARY;
		problem = '';
		phase = 'email';
	}

	// quit signs out on this device. The server revokes the session and
	// mints a fresh guest, so the address step returns with a new diary.
	async function quit() {
		busy = true;
		problem = '';
		try {
			await signOut(fetch);
			forgetSignedInEmail();
			email = '';
			code = '';
			notice = SIGNED_OUT;
			phase = 'email';
		} catch {
			problem = SIGN_OUT_FAILED;
		} finally {
			busy = false;
		}
	}

	$effect(() => {
		if (!browser) return;
		const stored = readSignedInEmail();
		if (stored !== null) {
			email = stored;
			phase = 'signedin';
		}
		return () => stopResendTimer();
	});
</script>

<svelte:head>
	<title>{pageTitle('Account')}</title>
	<meta
		name="description"
		content="Sign in with a mailed code. One device, one code, no password."
	/>
</svelte:head>

<main>
	<p class="eyebrow">Season one</p>
	<h1>{ACCOUNT_HEADING}</h1>
	<p class="sub">{ACCOUNT_SUB}</p>
	<nav aria-label="Season">
		<SeasonNav current="none" />
	</nav>

	{#if notice}
		<p role="status">{notice}</p>
	{/if}
	{#if problem}
		<p role="alert">{problem}</p>
	{/if}

	{#if phase === 'email' || phase === 'code'}
		<form
			onsubmit={(event) => {
				event.preventDefault();
				void (phase === 'email' ? send() : check());
			}}
		>
			{#if phase === 'email'}
				<div class="field">
					<Label.Root for="account-email">{EMAIL_LABEL}</Label.Root>
					<input
						id="account-email"
						type="email"
						autocomplete="email"
						bind:value={email}
						disabled={busy}
						required
					/>
					<p class="help">{EMAIL_HELP}</p>
				</div>
				<Button.Root type="submit" disabled={busy}>{SEND_CODE}</Button.Root>
			{:else}
				<div class="field">
					<Label.Root for="account-code">{CODE_LABEL}</Label.Root>
					<input
						id="account-code"
						inputmode="numeric"
						autocomplete="one-time-code"
						maxlength={6}
						bind:value={code}
						disabled={busy}
						required
					/>
				</div>
				<Button.Root type="submit" disabled={busy}>{CHECK_CODE}</Button.Root>
				<div class="resend">
					<Button.Root
						type="button"
						disabled={busy || resendWait > 0}
						onclick={() => void send()}
					>
						{resendWait > 0 ? `Wait ${resendWait} seconds` : RESEND_CODE}
					</Button.Root>
				</div>
			{/if}
		</form>
	{/if}

	{#if phase === 'conflict'}
		<section aria-label="Pick a diary">
			<h2>{CONFLICT_HEADING}</h2>
			<p>{CONFLICT_BODY}</p>
			<div class="choices">
				<div>
					<Button.Root type="button" disabled={busy} onclick={() => keep()}>{KEEP_DIARY}</Button.Root>
					<p class="help">{KEEP_HELP}</p>
				</div>
				<div>
					<Button.Root type="button" disabled={busy} onclick={() => void check('switch')}>
						{SWITCH_ACCOUNT}
					</Button.Root>
					<p class="help">{SWITCH_HELP}</p>
				</div>
			</div>
		</section>
	{/if}

	{#if phase === 'signedin'}
		<section aria-label="Signed in">
			<p role="status">{signedInNotice(email)}</p>
			<div class="choices">
				<Button.Root type="button" disabled={busy} onclick={() => void quit()}>{SIGN_OUT}</Button.Root>
				<!-- The deletion screen ships separately, so this entry waits here instead of stranding anyone. -->
				<div>
					<Button.Root type="button" disabled>{DELETE_ACCOUNT}</Button.Root>
					<p class="help">{DELETE_SOON}</p>
				</div>
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
