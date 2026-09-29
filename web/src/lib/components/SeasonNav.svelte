<script lang="ts">
	import { resolve } from '$app/paths';

	// The season tabs shared by the gallery and the threads pages. The
	// current tab fills with the accent, the other stays a plain pill.
	// The episode page passes none, so both tabs stay plain beside the
	// filled Edit pill it renders itself. The fixture flag keeps the
	// scripted season behind its query string.
	// The props carry current (gallery, threads, or none) and fixture.
	let { current, fixture = false } = $props();
</script>

<a
	class="tab"
	class:active={current === 'gallery'}
	href={fixture ? resolve('/?fixture=1') : resolve('/')}
	aria-current={current === 'gallery' ? 'page' : undefined}>Gallery</a
>
<a
	class="tab"
	class:active={current === 'threads'}
	href={fixture ? resolve('/threads?fixture=1') : resolve('/threads')}
	aria-current={current === 'threads' ? 'page' : undefined}>Threads</a
>

<style>
	.tab {
		display: inline-block;
		border: 1px solid var(--accent, #e8a33d);
		border-radius: 100px;
		padding: 0.55rem 1.1rem;
		font-weight: 600;
		color: var(--accent, #e8a33d);
		background: transparent;
		text-decoration: none;
	}
	.tab.active {
		background: var(--accent, #e8a33d);
		border-color: var(--accent, #e8a33d);
		color: var(--on-accent, #201809);
	}
	.tab:focus-visible {
		outline: 2px solid var(--accent, #e8a33d);
		outline-offset: 2px;
	}
</style>
