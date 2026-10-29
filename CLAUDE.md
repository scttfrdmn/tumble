# CLAUDE.md — tumble

Read `SETUP.md` first. It states the conventions and standing decisions for every
session. `PROJECT.md` is the design; `GIRI_REQUIREMENTS.md` is what tumble needs from
giri.

- Build: `make` (vet, build, test), `make lint`. The Makefile exports
  `GOEXPERIMENT=arenas`; outside make, set it yourself.
- Work is tracked on GitHub only: issues (with labels and a milestone) on
  `scttfrdmn/tumble`, and the `tumble` project board. Decisions and session reports
  are issue comments, not local files (see `SETUP.md`).
- Versions stay 0.x. Scott decides scope, versions, releases, and when a milestone
  or experiment is done.
