# Obsidian Center

Self-hosted sync server for Obsidian vaults with real-time CRDT-based collaboration.

## Quick Start (Docker)

```bash
# Clone and run
git clone https://github.com/Cyber-Mat/Obsidian-Center.git
cd Obsidian-Center
cp .env.example .env
# Edit .env and set a strong OC_JWT_SECRET

docker compose up -d
```

The server will be available at `http://localhost:8080`.

## Quick Start (Docker Hub)

```bash
docker run -d \
  -p 8080:8080 \
  -e OC_JWT_SECRET=change-me-to-a-strong-secret \
  -v obsidian-data:/data \
  cybermat/obsidian-center:latest
```

## Configuration

| Variable | Required | Default | Description |
|----------|----------|---------|-------------|
| `OC_JWT_SECRET` | Yes | - | Secret key for JWT token signing. Use a long random string. |

## Obsidian Plugin Setup

1. Copy the `plugin/` directory contents into your vault at `.obsidian/plugins/obsidian-center/`
2. Enable the plugin in Obsidian Settings > Community Plugins
3. In the plugin settings, set the server URL (e.g., `https://your-server.example.com`)
4. Register an account or log in
5. Create or select a vault to sync

## Architecture

See [ARCHITECTURE.md](ARCHITECTURE.md) for detailed technical documentation including:
- System design and data flow
- API reference
- Sync protocol details
- Development setup

## Development

```bash
# Server
cd server
go test ./...
go run ./cmd/server -jwt-secret=dev-secret -db=dev.db -addr=:8080

# Plugin
cd plugin
npm install
npm run dev

# Web editor
cd web
npm install
npm run dev
```

## Health Check

```bash
curl http://localhost:8080/api/health
```

## License

See [LICENSE](LICENSE) for details.
