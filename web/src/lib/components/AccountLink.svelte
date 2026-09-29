<script lang="ts">
	import { resolve } from '$app/paths';
	import { page } from '$app/state';
	import { ACCOUNT_EVENT, readSignedInEmail } from '../../routes/account/account';

	// The pill beside the season tabs. Guests read Sign in, and a
	// signed in device reads Account. The label follows local storage,
	// because no status route reports the session yet. The store reads
	// null without a stored address, so the first render says Sign in.
	// The store announces its own writes, and the storage event carries
	// the writes other tabs make. The pill fills while the account
	// screens show, the way the season tabs fill on their own pages.
	// It rides the shared secondary pill, so it matches the tabs.
	let address = $state(readSignedInEmail());

	// openPath reads the open path, or empty outside a request. Unit
	// mounts render with no request behind them, where the page store
	// has no path to report.
	function openPath() {
		try {
			return page.url.pathname;
		} catch {
			return '';
		}
	}

	let active = $derived.by(
		() => openPath() === '/account' || openPath().startsWith('/account/')
	);

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

<a
	class="button secondary"
	class:active
	href={resolve('/account')}
	aria-current={active ? 'page' : undefined}>{address === null ? 'Sign in' : 'Account'}</a
>
