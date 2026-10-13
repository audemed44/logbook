# Logbook

Instructions for coding agents working in this repository. `CLAUDE.md`
imports this file.

## Project

Every homelab container's logs in one place: search, follow a request, alert on error bursts, with hard storage limits. A Go server (`cmd/logbook`, `internal/`)
serves a JSON API and the Preact + TypeScript frontend (`frontend/`), built
into `web/dist` and embedded in the binary. State lives in SQLite at
`/data/logbook.db` (`internal/store`).

- `internal/docker`: read-only Docker API client (list, inspect, follow
  logs, events) over the unix socket.
- `internal/parse`: one line to an entry: JSON, logfmt, plain text with a
  guessed level; what counts as a continuation line.
- `internal/collect`: a follower per running container, the per-container
  limits (`gate.go`: repeats, per minute, daily quota, flood skip-ahead),
  batched writes, and the hub that feeds live tails.
- `internal/watch`: the upkeep every minute: prune to the size cap and
  retention, error-burst alerts, the heartbeat.
- `internal/server`: routes (`server.go`), the log API (`logs.go`), token
  sign-in and the same-origin guard (`auth.go`), the Foyer card
  (`foyer.go`).
- `internal/logging`: JSON logs for Logbook, the request middleware,
  `Job`/`QuietJob` for background work, `Audit` for what people do, and
  the limits that stop a failing app flooding the disk.
- `internal/store`: schema (lines, an FTS5 index, hourly counts per
  source), migrations (append-only, tracked in `PRAGMA user_version`),
  queries, and `Prune`.
- `frontend/src`: `App.tsx` (session, shell, nav), `router.ts` (path
  routes; the logs page keeps its filters in the query string), `api.ts`,
  pages in `components/` (Overview, Logs, Trace, Sources, About),
  `LineList.tsx` (rows that open to all fields), `HourChart.tsx`,
  `styles.css`.

## Constraints

- **Low memory is a feature.** One static binary, `GOMEMLIMIT=32MiB`,
  `mem_limit: 64m` in compose. Keep history in SQLite, not in memory.
- Direct dependencies: modernc.org/sqlite (pure Go, so the build stays
  static and cgo-free). Justify any new one, Go or npm.
- Every `/api/` call needs `LOGBOOK_TOKEN` (bearer, or the session cookie
  derived from it), and state-changing browser requests from another origin
  are refused (`sameOrigin`). Never log or return secrets.
- `GET /api/foyer/widget` serves the card in Foyer's widget format
  (https://github.com/audemed44/foyer/blob/main/docs/app-widgets.md).
- `HOMEPAGE_URL` puts a link back to Foyer in the header.
- UI style is Foyer's: Swiss editorial, always dark (no light theme), heavy
  Inter headlines, tracked uppercase eyebrows, 2px rules over numbered
  headings, square corners, one accent (#2563ff). Check phone width too
  (390px): it's used from an iPhone.
- **Limits are the point.** Anything a container can make Logbook store or
  do must be bounded: rows, bytes per row, work per line. A new feature
  that reads or keeps more needs its own cap, with a default.
- Logbook never logs per collected line (that would feed itself); its own
  write and upkeep jobs are `QuietJob`s.
- The Docker client sends GET requests only.

## Logging

Use `internal/logging`, through `log/slog`:

- Inside a request or job, log with the `Context` variants
  (`slog.ErrorContext(r.Context(), …)`) so the line carries its trace.
- Wrap background work in `logging.Job` (or `QuietJob` when it runs every
  minute or so) and call `End(err, counts…)`.
- Call `logging.Audit(ctx, "thing.verb", ids…)` for anything a person
  changes: sign-ins, settings, deletes. Set `logging.SetUser` where the app
  knows who's signed in.
- Messages are short and stable (`"mail sync failed"`, not one with an id
  in it); details go in attributes. The limits count repeats per message.
- Ids, counts and names only. Never tokens, passwords or the content the
  app stores (mail bodies, amounts, document text).

## Commits

Conventional Commits: `<type>(<scope>): <summary>`, e.g. `feat(items): ...`.
CI rejects anything else, including the PR title.

## Checks before pushing

```sh
go vet ./... && go test -race ./...        # needs web/dist (npm run build)
cd frontend && npm run format:check && npm run typecheck && npm test && npm run build
docker build -t logbook:dev .
```
