<!-- Commit + PR title follow the fleet commit convention (glyph.toml is the
     grammar; `glyph lint --pr N` checks the title):
     https://github.com/akira-toriyama/.github/blob/main/CONTRIBUTING.md -->

## What & why

<!-- One or two lines: what changed and the reason. -->

## Checks

- [ ] `sh scripts/check.sh` passes locally (build / vet / test -race / smoke)
- [ ] Docs updated if user-visible behavior changed (README.md — English-only, code-first)

<!-- Optional: link a projects task so its status tracks this PR (open →
     in-progress, merge → the given lane). Drop the lane to only reference it.
SetStatus-task: https://github.com/akira-toriyama/projects/blob/main/.furrow/bodies/<id>.md in-progress
-->
