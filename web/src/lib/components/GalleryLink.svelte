<script lang="ts">
	import { resolve } from '$app/paths';
</script>

<!-- One way back to the season list. Every page uses this link, so the -->
<!-- label and the shape never drift. It rides the shared secondary pill, -->
<!-- so it matches every other outlined action. It stays an anchor, so -->
<!-- middle-click and keyboard habits keep working. -->
<a class="button secondary" href={resolve('/')}
	><span aria-hidden="true">←</span> Back to the gallery</a
>
