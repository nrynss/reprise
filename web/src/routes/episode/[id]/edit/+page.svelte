<script lang="ts">
	import { browser } from '$app/environment';
	import { page } from '$app/state';
	import { activeWordAt } from '@nrynss/chaaya/transcript';
	import DemoBadge from '$lib/components/DemoBadge.svelte';
	import GalleryLink from '$lib/components/GalleryLink.svelte';
	import { pageTitle } from '$lib/shell';
	import { DraftController, emptyDraft, formatTime, queryValue, runEditorGates } from '$lib/editor/draft';

	let snap = $state(emptyDraft(page.params.id ?? 'draft'));
	let controller = $state<DraftController | null>(null);
	let wave = $state<HTMLCanvasElement | null>(null);
	let dragging = $state(false);

	$effect(() => {
		if (!browser) return;
		const next = new DraftController({
			episodeId: page.params.id ?? 'draft',
			onChange: (fresh) => {
				snap = fresh;
			}
		});
		controller = next;
		next.mount(window.location.search);
		if (queryValue(window.location.search, 'gate') === '1') {
			const main = document.querySelector('main');
			if (main) {
				void runEditorGates(main).then((result) => {
					next.setGateResult(result);
				});
			}
		}
		return () => {
			next.destroy();
			controller = null;
		};
	});

	// Keep the playhead out of removed spans while the draft plays. The
	// follower seeks past a cut once, then reports quiet. The length
	// follows the element once it reports one, so the readout never runs
	// past the audio it describes.
	$effect(() => {
		if (!browser || !controller) return;
		const clock = controller.player;
		void clock.currentTime;
		void clock.duration;
		controller.syncDuration();
		if (clock.playing) controller.follower?.update();
	});

	// One position drives the readout, the playhead and the seekbar,
	// so the waveform and the range never disagree. It clamps to the
	// adopted length, which follows the element once it reports one.
	let position = $derived.by(() => {
		const raw = controller ? controller.player.currentTime || snap.position : 0;
		return Math.min(raw, snap.duration);
	});

	// Draw peaks once the worker answers, with the played span under
	// them and the removed ranges over them. The playhead line and its
	// handle sit on top, so one glance shows the audio, the position
	// and what a drag would move. Regions come from the cuts, so the
	// marks never drift from the transcript.
	$effect(() => {
		if (!browser || !wave || !controller) return;
		void controller.player.currentTime;
		void controller.player.duration;
		const current = snap;
		if (!current.ready) return;
		const canvas = wave;
		const width = (canvas.width = 600);
		const height = (canvas.height = 64);
		const drawing = canvas.getContext('2d');
		if (!drawing) return;
		const span = current.duration > 0 ? Math.max(0, Math.min(1, position / current.duration)) : 0;
		const head = span * width;
		drawing.clearRect(0, 0, width, height);
		drawing.fillStyle = 'rgba(232, 163, 61, 0.22)';
		drawing.fillRect(0, 0, head, height);
		drawing.fillStyle = '#e8a33d';
		const buckets = current.peaks?.max.length ?? 0;
		if (buckets > 0 && current.peaks) {
			for (let bucket = 0; bucket < buckets; bucket += 1) {
				const max = Math.max(0, current.peaks.max[bucket] ?? 0);
				const min = Math.min(0, current.peaks.min[bucket] ?? 0);
				const bar = Math.max(1, ((max - min) / 2) * height);
				drawing.fillRect((bucket / buckets) * width, (height - bar) / 2, width / buckets, bar);
			}
		} else {
			drawing.fillRect(0, height / 2 - 1, width, 2);
		}
		drawing.fillStyle = 'rgba(210, 112, 95, 0.55)';
		for (const region of current.regions) {
			const from = (region.start / current.duration) * width;
			const to = (region.end / current.duration) * width;
			drawing.fillRect(from, 0, Math.max(2, to - from), height);
		}
		const line = Math.max(0, Math.min(width - 1, head));
		drawing.fillStyle = '#f4edde';
		drawing.fillRect(line - 1, 0, 2, height);
		drawing.beginPath();
		drawing.arc(Math.max(5, Math.min(width - 5, line)), 6, 5, 0, Math.PI * 2);
		drawing.fill();
	});

	let liveWord = $derived.by(
		() =>
			controller && snap.ready
				? (activeWordAt(snap.words, snap.cuts, controller.player.currentTime || snap.position) ??
					activeWordAt(snap.words, snap.cuts, snap.position))
				: null
	);
</script>

<svelte:head>
	<title>{pageTitle(snap.title || 'Editor')}</title>
</svelte:head>

<main class="wide">
	<GalleryLink />
	<p class="eyebrow">{snap.episodeLabel}</p>
	<h1>{snap.title || 'Editor'}</h1>
	{#if snap.demo}<DemoBadge />{/if}
	<p role="status" aria-label="Applied cuts">{snap.appliedCount} cuts applied</p>
	<p role="status">{snap.notice}</p>

	{#if !snap.ready && snap.loadError}
		<div class="actions">
			<button class="button secondary" onclick={() => controller?.retry()}>Retry</button>
		</div>
	{/if}

	{#if snap.ready}
		<section class="section" aria-label="Draft playback">
			<h2>Playback</h2>
			<div class="actions">
				<button
					class="button"
					aria-label={controller?.player.playing ? 'Pause draft' : 'Play draft'}
					onclick={() => void controller?.togglePlay()}
				>
					{controller?.player.playing ? 'Pause draft' : 'Play draft'}
				</button>
				<button
					class="button secondary"
					onclick={() => controller?.seekTo(position - 15)}
					aria-label="Back fifteen seconds"
				>
					Back 15
				</button>
			</div>
			<canvas
				bind:this={wave}
				class:dragging
				role="slider"
				tabindex="0"
				aria-label="Draft waveform. Arrow keys seek."
				aria-valuemin={0}
				aria-valuemax={Math.round(snap.duration)}
				aria-valuenow={Math.round(position)}
				aria-valuetext={`${formatTime(position)} of ${formatTime(snap.duration)}`}
				onkeydown={(event) => {
					if (event.key === 'ArrowLeft') controller?.seekTo(position - 5);
					else if (event.key === 'ArrowRight') controller?.seekTo(position + 5);
					else if (event.key === 'Home') controller?.seekTo(0);
					else if (event.key === 'End') controller?.seekTo(snap.duration);
				}}
				onpointerdown={(event) => {
					dragging = true;
					wave?.setPointerCapture(event.pointerId);
					const width = wave?.clientWidth || 1;
					controller?.seekTo((event.offsetX / width) * snap.duration);
				}}
				onpointermove={(event) => {
					if (event.buttons === 1) {
						const width = wave?.clientWidth || 1;
						controller?.seekTo((event.offsetX / width) * snap.duration);
					}
				}}
				onpointerup={() => {
					dragging = false;
				}}
				onpointercancel={() => {
					dragging = false;
				}}
			></canvas>
			<input
				type="range"
				min={0}
				max={snap.duration}
				step={1}
				value={Math.round(position)}
				oninput={(event) => controller?.seekTo(Number(event.currentTarget.value))}
				aria-label="Seek through the draft"
			/>
			<p role="status" aria-label="Playback position">{formatTime(position)} of {formatTime(snap.duration)}</p>
		</section>

		<section class="section" aria-label="Transcript. Select a word to seek.">
			<h2>Transcript</h2>
			<p class="transcript words">
				{#each snap.words as word, index (index)}
					{@const cutId = snap.cutOf[index]}
					{@const reason = cutId
						? (snap.cutCards.find((card) => card.id === cutId)?.reason ?? '')
						: ''}
					{#if cutId}
						<s><button
							aria-label={`${word.text}, cut: ${reason}. Activate to seek.`}
							aria-current={liveWord === index ? 'true' : undefined}
							class:live={liveWord === index}
							onclick={() => controller?.seekToWord(index)}
						>{word.text}</button></s>
					{:else}
						<button
							aria-label={`${word.text}. Activate to seek.`}
							aria-current={liveWord === index ? 'true' : undefined}
							class:live={liveWord === index}
							onclick={() => controller?.seekToWord(index)}
						>{word.text}</button>
					{/if}
					<!-- The space between words is an expression so the compiler keeps it. -->
					<!-- eslint-disable-next-line svelte/no-useless-mustaches -->
					{' '}
				{/each}
			</p>
		</section>

		<div aria-label="Suggestions">
			{#if snap.hasColdOpen}
				<section class="section" aria-label="Cold open">
					<h2>Cold open</h2>
					{#if snap.coldOpenReverted}
						<p><s>{snap.coldOpen.quote}</s></p>
						<p>{snap.coldOpen.reason}</p>
						<p>Cold open reverted. The episode starts at the top.</p>
					{:else}
						<blockquote>{snap.coldOpen.quote}</blockquote>
						<p>{snap.coldOpen.reason}</p>
						<div class="actions">
							<button
								class="button secondary"
								aria-label="Preview the cold open"
								onclick={() => void controller?.previewColdOpen()}
							>
								Preview the cold open
							</button>
							<button
								class="button quiet"
								aria-label="Revert cold open"
								onclick={() => controller?.revertColdOpen()}
							>
								Revert the cold open
							</button>
						</div>
					{/if}
				</section>
			{/if}

			<section class="section" aria-label="Title">
				<h2>Title</h2>
				{#if snap.titleReverted}
					<p><s>{snap.proposedTitle}</s></p>
					<p>Title reverted. The heading shows the plain episode number.</p>
				{:else}
					<p>{snap.proposedTitle}</p>
					<div class="actions">
						<button
							class="button quiet"
							aria-label="Revert title"
							onclick={() => controller?.revertTitle()}
						>
							Revert the title
						</button>
					</div>
				{/if}
			</section>

			<section class="section" aria-label="Show notes">
				<h2>Show notes</h2>
				{#if snap.notesReverted}
					<p><s>{snap.proposedNotes}</s></p>
					<p>Show notes reverted. Nothing stands in their place.</p>
				{:else}
					<p>{snap.notes}</p>
					<div class="actions">
						<button
							class="button quiet"
							aria-label="Revert show notes"
							onclick={() => controller?.revertNotes()}
						>
							Revert the show notes
						</button>
					</div>
				{/if}
			</section>

			<section class="section" aria-label="Cuts">
				<h2>Cuts</h2>
				{#if snap.cutCards.length === 0}
					<p>Every cut is reverted.</p>
				{:else}
					<ol>
						{#each snap.cutCards as card (card.id)}
							<li>
								<p>{card.reason}</p>
								<p>{card.quote}</p>
								<div class="actions">
									<button
										class="button quiet"
										aria-label={`Revert cut: ${card.reason}`}
										onclick={() => controller?.revertCut(card.id)}
									>
										Revert this cut
									</button>
								</div>
							</li>
						{/each}
					</ol>
				{/if}
			</section>

			{#if snap.callback || snap.callbackQuote || snap.callbackReverted}
				<section class="section" aria-label="Next time">
					<h2>Next time</h2>
					{#if snap.callbackReverted}
						<p><s>{snap.proposedCallback}</s></p>
						<p>Callback reverted and cleared from the next opening.</p>
					{:else}
						<p>{snap.callback}</p>
						<p>{snap.callbackQuote}</p>
						<div class="actions">
							<button
								class="button quiet"
								aria-label="Revert callback"
								onclick={() => controller?.revertCallback()}
							>
								Revert the callback
							</button>
						</div>
					{/if}
				</section>
			{/if}

			<section class="section" aria-label="Decisions">
				<h2>Decisions</h2>
				{#if snap.decisions.length === 0}
					<p>No changes yet.</p>
				{:else}
					<ol>
						{#each snap.decisions as row (row.id)}
							<li>Reverted. {row.reason}</li>
						{/each}
					</ol>
				{/if}
			</section>
		</div>

		<section class="section" aria-label="Finish the draft">
			<h2>Mark done</h2>
			<p>Finishing makes the episode ready. Every cut can still be reverted.</p>
			{#if snap.renderStage === 'idle'}
				<div class="actions">
					<button class="button" aria-label="Mark episode done" onclick={() => controller?.markDone()}>
						Mark episode done
					</button>
				</div>
			{:else if snap.renderStage === 'confirm'}
				<div class="actions">
					<button class="button" aria-label="Confirm mark done" onclick={() => controller?.markDone()}>
						Confirm mark done
					</button>
					<button
						class="button secondary"
						aria-label="Cancel mark done"
						onclick={() => controller?.cancelMarkDone()}
					>
						Cancel
					</button>
				</div>
			{:else}
				<p role="status">{snap.renderDetail}</p>
			{/if}
		</section>

		{#if snap.gateResult}
			<p id="gate-status" role="status">{snap.gateResult}</p>
		{/if}
	{/if}
</main>

<style>
	/* Word buttons stay inline text, never the shared pill. The pill
	belongs to player and suggestion actions alone. */
	.words button {
		font: inherit;
		background: transparent;
		border: none;
		color: var(--ink);
		padding: 0.1rem 0.2rem;
		margin: 0 0.3rem 0 0;
		border-radius: 0.25rem;
		min-height: 0;
		display: inline;
		cursor: pointer;
	}
	.words s button {
		text-decoration: line-through;
		color: var(--muted);
	}
	button.live {
		outline: 2px solid var(--accent);
		outline-offset: 1px;
	}
	canvas {
		display: block;
		width: 100%;
		height: 4rem;
		margin-top: 1rem;
		border: 1px solid var(--line);
		border-radius: 0.5rem;
		cursor: pointer;
	}
	canvas.dragging {
		cursor: grabbing;
	}
	canvas:focus-visible {
		outline: 3px solid var(--ink);
		outline-offset: 2px;
	}
	input[type='range'] {
		width: 100%;
		margin: 0.75rem 0;
		accent-color: var(--accent);
	}
	blockquote {
		border-left: 2px solid var(--accent);
		padding-left: 1rem;
		color: var(--ink);
	}
</style>
