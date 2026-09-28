<script lang="ts">
	import { resolve } from '$app/paths';
</script>

<!-- One way back to the season list. Every page uses this link, so the -->
<!-- label and the shape never drift. It stays an anchor, so middle-click -->
<!-- and keyboard habits keep working. -->
<a class="gallery-link" href={resolve('/')}><span aria-hidden="true">←</span> Back to the gallery</a>

<style>
	.gallery-link {
		display: inline-block;
		border: 1px solid var(--accent, #e8a33d);
		border-radius: 100px;
		padding: 0.55rem 1.1rem;
		font-weight: 600;
		color: var(--accent, #e8a33d);
		background: transparent;
		text-decoration: none;
	}
	.gallery-link:hover {
		border-color: var(--ink, #f4edde);
		color: var(--ink, #f4edde);
	}
	.gallery-link:focus-visible {
		outline: 2px solid var(--accent, #e8a33d);
		outline-offset: 2px;
	}
</style>
