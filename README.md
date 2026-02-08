# Beadboard - Gas Town Bead Viewer

Three-panel bead viewer for Gas Town. Shows ready/in-progress beads across all rigs, filters system noise, and provides copyable commands for bead management.

## Run

### Go (recommended)

```bash
# Build
go build -o bin/beadboard-go .

# Run
./bin/beadboard-go

# Custom port
./bin/beadboard-go --port 3000

# Open browser automatically
./bin/beadboard-go --open
```

### Node.js

```bash
node server.js

# Or via wrapper
./bin/beadboard

# Custom port
./bin/beadboard --port 3000

# Open browser automatically
./bin/beadboard --open
```

Default: http://localhost:9292

## Config

Edit `config.json` to change filters, port, or refresh interval. Changes can also be made from the UI (persisted to config.json).

## API

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/` | GET | Main UI |
| `/api/ready` | GET | Ready beads across town (gt ready) |
| `/api/status` | GET | Town state - rigs, agents, hooks (gt status) |
| `/api/beads?status=X&type=Y` | GET | Filtered bead listing (bd list) |
| `/api/bead/:id` | GET | Single bead detail (bd show) |
| `/api/config` | GET | Current filter config |
| `/api/config` | POST | Update filter config |
| `/health` | GET | Health check |
