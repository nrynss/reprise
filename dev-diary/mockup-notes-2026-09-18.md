# 2026-09-18 · Reprise — PRD → UI design

## Task
User attached a full PRD set (12 docs) for **Reprise** (personal podcast; AI host with cross-episode memory; hackathon build on SvelteKit + Go, Keel/Chaaya libraries). Asked for PRD breakdown + the corresponding UI design.

## Process
- Read all 12 attached docs (product, project, README, libraries, PHASE-0..6, AGENTS).
- Not a Redesign (nothing built yet). PRD branch → module/screen breakdown first.
- Asked one minimal question-form (visual direction, platform, scope, depth).
- Answers: **Night Studio** · both platforms equally · all screens · deep interactive.
- Loaded required references before writing: prototype skeleton + layouts/checklist, craft set
  (typography, color, anti-ai-slop, accessibility, state-coverage, laws-of-ux, animation, form-validation, typography-hierarchy),
  output frameworks (designer-prompt, discovery-philosophy), infinite canvas skill + template.

## Deliverables (workspace root)
- `V1 Reprise-Interactive Prototype.html` — 11 views, simulated live session, revertible cuts, playback sync, publish/erase/export gates, admin switches, prototype state lab.
- `V1 Reprise-Infinite Canvas.html` — 3 rows: project context + flow/memory-loop diagram, design system, 11 screen designs in flow order with module groups, state notes.

## Design system (Night Studio)
- bg #17130F · surface #201A13 · raised #2A221A · amber #E8A33D · sienna #A5714B · cream #F2EAD9 · on-air #D9543F · ready #8FA874.
- Instrument Serif (display) + system sans (UI) + JetBrains Mono (timecodes/levels).
- Demo content: seeded season by "Dana" — June (sister), Tomas, Margot, the allotment, the harvest;
  host named Rhea; episode five opens on the ep-1 callback.

## Environment notes / caveats
- `exec` in this session returned no output and did not run commands (a redirect-to-file check produced no file).
  → No programmatic/JS-syntax or browser render verification possible. Verification was static (full re-read of both files).
- `canvas` tool needs a paired node; none available → no screenshot check.
- Both files are self-contained; fonts load from Google Fonts with system fallbacks.
