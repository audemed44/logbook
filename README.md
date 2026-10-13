# Logbook

Every homelab container's logs in one place: search, follow a request, alert on error bursts, with hard storage limits.

It follows every running container's output through the Docker socket,
keeps it in SQLite, and gives you:

- **Search** across every container: by container, level, kind (request,
  job, audit, docker), time and words in the message, error or fields.
- **Live tail** of new lines as they're stored.
- **Traces**: every line of one request or job run, across containers,
  timed from the first.
- **Problems**: warnings and errors in the last 24 hours, grouped by
  message, and errors per hour.
- **Container events** as lines: starts, stops, crashes with their exit
  code, out-of-memory kills.
- **Alerts** through Lookout when a container logs a burst of errors or hits
  its daily quota, a heartbeat, and a Foyer card.

JSON lines (the homelab apps' `internal/logging`, and most other JSON
loggers) keep their level, message, trace, user and fields. logfmt is read
too; plain text gets a level guessed from the line, and indented stack
frames and Python tracebacks are joined to the line they belong to.

One small Go binary with the web UI built in, about 25 MB of memory.

## Limits

A failing app must not be able to fill the disk, or keep Logbook busy.
Every limit has a default:

| | Default | |
|---|---|---|
| `LOGBOOK_MAX_SIZE` | `256MB` | Database size cap. Past it, the oldest lines are deleted (down to 90%) and the file shrinks |
| `LOGBOOK_RETENTION` | `30d` | Lines older than this are deleted |
| `LOGBOOK_DAILY_QUOTA` | `20000` | Lines per container per day; past it, one line says so and the rest are dropped until midnight |
| `LOGBOOK_PER_MINUTE` | `600` | Lines per container per minute |
| `LOGBOOK_PER_MESSAGE` | `30` | Repeats of one message (numbers aside) per container per minute; the rest are counted, with one summary line |
| `LOGBOOK_MAX_LINE` | `8192` | Bytes kept of a message, an error or the fields |

A container writing more than 10 × the per-minute limit is a flood: Logbook
stops reading it until the minute ends and then continues from the end of
its log, so the flood costs next to nothing (tested: a container writing as
fast as it could for 30 seconds cost Logbook 0.45 s of CPU and 8 stored
lines). What the limits drop is counted per hour and shown as "held back".

Docker keeps its own copy of every container's logs, with no limit unless
compose sets one: give each service a `logging:` cap (see
[docker-compose.example.yml](docker-compose.example.yml)).

## Run it

See [docker-compose.example.yml](docker-compose.example.yml): the socket
mount, the docker group, and every option.

| Variable | Default | |
|---|---|---|
| `LOGBOOK_TOKEN` | (required) | What you sign in with; also Foyer's widget key |
| `LOGBOOK_DATA_DIR` | `/data` | Where the database lives |
| `LOGBOOK_PORT` | `8080` | Port inside the container |
| `LOGBOOK_DOCKER_SOCKET` | `/var/run/docker.sock` | |
| `LOGBOOK_EXCLUDE` | | Containers to leave out, comma separated (or label one `logbook.exclude=true`) |
| `LOGBOOK_BACKFILL` | `1h` | How far back to read a container seen for the first time |
| `LOGBOOK_NOTIFY_URL` | | Alerts, Apprise JSON (Lookout's `/notify/<key>`) |
| `LOGBOOK_ALERT_ERRORS` | `10` | Errors from one container in 5 minutes that send an alert (one an hour at most) |
| `LOGBOOK_HEARTBEAT_URL` | | Pinged every minute after a healthy upkeep (Lookout's `/ping/<token>`) |
| `LOGBOOK_URL` | | Logbook's address, for links in alerts |
| `HOMEPAGE_URL` | | Foyer's address, linked from the header |
| `LOG_LEVEL` | `info` | `debug`, `info`, `warn` or `error` |
| `LOG_FORMAT` | `json` | `text` for reading in a terminal |

The socket gives more than reading in principle (`:ro` doesn't restrict the
API); Logbook only ever sends GET requests for containers, logs and events.

## API

Every call takes the token as a bearer token.

- `GET /api/lines?source=&level=&kind=&q=&trace=&since=&until=&before=&limit=`:
  newest first; `next` in the answer continues as `before=`.
- `GET /api/tail?…`: the same filters, as server-sent events.
- `GET /api/lines/{id}/around`: the container's lines around one line.
- `GET /api/sources`, `GET /api/overview`, `GET /api/foyer/widget`.

## Foyer

Logbook serves a [Foyer](https://github.com/audemed44/foyer) card at
`/api/foyer/widget`:

```yaml
      - name: Logbook
        url: https://logbook.example.com
        container: logbook
        widget:
          type: app
          url: http://logbook:8080/api/foyer/widget
          key: ${LOGBOOK_TOKEN}
```

## Development

```sh
cd frontend && npm install && npm run build && cd ..
LOGBOOK_TOKEN=dev LOGBOOK_DATA_DIR=./data go run ./cmd/logbook
# or, with hot reload: run the binary, then `npm run dev` in frontend/
```
