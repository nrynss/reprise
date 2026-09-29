<script lang="ts">
	import { resolve } from '$app/paths';
	import { ACCOUNT_EVENT, readSignedInEmail } from '../../routes/account/account';

	// The quiet link beside the season tabs. Guests read Sign in, and a
	// signed in device reads Account. The label follows local storage,
	// because no status route reports the session yet. The store reads
	// null without a stored address, so the first render says Sign in.
	// The store announces its own writes, and the storage event carries
	// the writes other tabs make.
	let address = $state(readSignedInEmail());

	$effect(() => {
		const refresh = () => {
			address = readSignedInEmail();
		};
		window.addEventListener('storage', refresh);
		window.addEventListener(ACCOUNT_EVENT, refresh);
		return () => {
			window.removeEventListener('storage', refresh);
			window.removeEventListener(ACCOUNT_EVENT, refresh);
		};
	});
</script>

<a class="account-link" href={resolve('/account')}>{address === null ? 'Sign in' : 'Account'}</a>

<style>
	.account-link {
		display: inline-block;
		padding: 0.55rem 0.2rem;
		font-weight: 600;
		color: inherit;
		opacity: 0.8;
		text-decoration: underline;
		text-underline-offset: 3px;
	}
	.account-link:hover {
		opacity: 1;
	}
	.account-link:focus-visible {
		outline: 2px solid var(--accent, #e8a33d);
		outline-offset: 2px;
	}
</style>
