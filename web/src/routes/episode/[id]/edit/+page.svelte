<script lang="ts">
	import { browser } from '$app/environment';
	import { page } from '$app/state';
	import { activeWordAt } from '@nrynss/chaaya/transcript';
	import { pageTitle } from '$lib/shell';
	import { DraftController, emptyDraft, formatTime, queryValue, runEditorGates } from '$lib/editor/draft';

	let snap = $state(emptyDraft(page.params.id ?? 'draft'));
	let controller = $state<DraftController | null>(null);
	let wave = $state<HTMLCanvasElement | null>(null);

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
	// follower seeks past a cut once, then reports quiet.
	$effect(() => {
		if (!browser || !controller) return;
		const clock = controller.player;
		void clock.currentTime;
		if (clock.playing) controller.follower?.update();
	});

	// Draw peaks once the worker answers, with removed ranges shaded over
	// them. Regions come from the cuts, so the marks never drift from the
	// transcript.
	$effect(() => {
		if (!browser || !wave || !controller) return;
		const current = snap;
		if (!current.ready) return;
		const canvas = wave;
		const width = (canvas.width = 600);
		const height = (canvas.height = 64);
		const drawing = canvas.getContext('2d');
		if (!drawing) return;
		drawing.clearRect(0, 0, width, height);
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
	});

	let liveWord = $derived.by(
		() =>
			controller && snap.ready
				? (activeWordAt(snap.words, snap.cuts, controller.player.currentTime || snap.position) ??
					activeWordAt(snap.words, snap.cuts, snap.position))
				: null
	);

	let position = $derived.by(() => (controller ? controller.player.currentTime || snap.position : 0));
</script>

<svelte:head>
	<title>{pageTitle(snap.title || 'Editor')}</title>
</svelte:head>

<main>
	<p class="eyebrow">{snap.episodeLabel}</p>
	<h1>{snap.title || 'Editor'}</h1>
	<p role="status" aria-label="Applied cuts">{snap.appliedCount} cuts applied</p>
	<p role="status">{snap.notice}</p>

	{#if snap.ready}
		<section aria-label="Draft playback">
			<h2>Playback</h2>
			<button
				aria-label={controller?.player.playing ? 'Pause draft' : 'Play draft'}
				onclick={() => void controller?.togglePlay()}
			>
				{controller?.player.playing ? 'Pause draft' : 'Play draft'}
			</button>
			<canvas
				bind:this={wave}
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
			></canvas>
			<p role="status" aria-label="Playback position">{formatTime(position)} of {formatTime(snap.duration)}</p>
		</section>

		<section aria-label="Transcript. Click a word to seek. Strikethrough marks a proposed cut.">
			<h2>Transcript</h2>
			<p class="words">
				{#each snap.words as word, index (index)}
					{@const cutId = snap.cutOf[index]}
					{@const reason = cutId
						? (snap.cutCards.find((card) => card.id === cutId)?.reason ?? '')
						: ''}
					{#if cutId}
						<s><button
							aria-label={`${word.text}, proposed cut: ${reason}. Activate to seek.`}
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
				{/each}
			</p>
		</section>

		<aside aria-label="Proposals">
			<section aria-label="Proposed cold open">
				<h2>Proposed cold open</h2>
				<blockquote>{snap.coldOpen.quote}</blockquote>
				<p>{snap.coldOpen.reason}</p>
				<button
					aria-label="Preview the cold open"
					onclick={() => void controller?.previewColdOpen()}
				>
					Preview the cold open
				</button>
			</section>

			<section aria-label="Proposed cuts, applied by default">
				<h2>Proposed cuts</h2>
				{#if snap.cutCards.length === 0}
					<p>Every proposed cut is reverted.</p>
				{:else}
					<ol>
						{#each snap.cutCards as card (card.id)}
							<li>
								<p>{card.reason}</p>
								<p>{card.quote}</p>
								<button
									aria-label={`Revert cut: ${card.reason}`}
									onclick={() => controller?.revertCut(card.id)}
								>
									Revert this cut
								</button>
							</li>
						{/each}
					</ol>
				{/if}
			</section>

			<section aria-label="Planted for next time">
				<h2>Planted for next time</h2>
				<p>{snap.callback}</p>
				<p>{snap.callbackQuote}</p>
			</section>

			<section aria-label="Decisions">
				<h2>Decisions</h2>
				{#if snap.decisions.length === 0}
					<p>No decisions yet. Reverting a cut writes its row here.</p>
				{:else}
					<ol>
						{#each snap.decisions as row (row.id)}
							<li>Reverted: {row.reason} ({row.proposalId})</li>
						{/each}
					</ol>
				{/if}
			</section>
		</aside>

		<section aria-label="Finish the draft">
			<h2>Mark done</h2>
			<p>Marking done renders the episode and runs analysis once. Every cut stays revertible until then.</p>
			{#if snap.renderStage === 'idle'}
				<button aria-label="Mark episode done" onclick={() => controller?.markDone()}>
					Mark episode done
				</button>
			{:else if snap.renderStage === 'confirm'}
				<button aria-label="Confirm mark done" onclick={() => controller?.markDone()}>
					Confirm mark done
				</button>
				<button aria-label="Cancel mark done" onclick={() => controller?.cancelMarkDone()}>
					Cancel
				</button>
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
	:root {
		color-scheme: dark;
		--paper: #191410;
		--raised: #241d15;
		--ink: #f4edde;
		--muted: #d9cfbb;
		--accent: #e8a33d;
		--on-accent: #201809;
		--line: #5a4f41;
	}
	main {
		max-width: 60rem;
		margin: 0 auto;
		padding: 3rem 1.5rem 5rem;
		font-family: system-ui, sans-serif;
		background: var(--paper);
		color: var(--ink);
	}
	.eyebrow {
		font-size: 0.75rem;
		letter-spacing: 0.08em;
		text-transform: uppercase;
		color: var(--muted);
	}
	section {
		background: var(--raised);
		border: 1px solid var(--line);
		border-radius: 0.75rem;
		padding: 1.25rem;
		margin-top: 1.25rem;
	}
	button {
		font: inherit;
		color: var(--on-accent);
		background: var(--accent);
		border: none;
		border-radius: 0.5rem;
		padding: 0.6rem 1rem;
		margin: 0.15rem;
		cursor: pointer;
	}
	button:focus-visible {
		outline: 3px solid var(--ink);
		outline-offset: 2px;
	}
	p button {
		background: transparent;
		color: var(--ink);
		padding: 0.1rem 0.2rem;
		margin: 0 0.3rem 0 0;
		border-radius: 0.25rem;
	}
	p s button {
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
	}
	canvas:focus-visible {
		outline: 3px solid var(--ink);
		outline-offset: 2px;
	}
	blockquote {
		border-left: 2px solid var(--accent);
		padding-left: 1rem;
		color: var(--ink);
	}
	p,
	li {
		color: var(--ink);
	}
</style>
