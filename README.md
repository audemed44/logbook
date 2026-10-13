# Logbook

Every homelab container's logs in one place: search, follow a request, alert on error bursts, with hard storage limits.

One small Go binary with the web UI built in, data in SQLite.

## Run it

```yaml
services:
  logbook:
    image: ghcr.io/audemed44/logbook:latest
    restart: unless-stopped
    user: "1000:1000"
    environment:
      - LOGBOOK_TOKEN=${LOGBOOK_TOKEN} # openssl rand -hex 32
    volumes:
      - ./logbook:/data
    ports:
      - "8092:8080"
```

See [docker-compose.example.yml](docker-compose.example.yml) for every option.

| Variable | Default | |
|---|---|---|
| `LOGBOOK_TOKEN` | (required) | What you sign in with; also Foyer's widget key |
| `LOGBOOK_DATA_DIR` | `/data` | Where the database lives |
| `LOGBOOK_PORT` | `8080` | Port inside the container |
| `HOMEPAGE_URL` | | Foyer's address, linked from the header |
| `LOGBOOK_DEBUG` | | Set to log debug messages |

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
