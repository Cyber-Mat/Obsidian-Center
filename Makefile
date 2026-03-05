.PHONY: build run test clean dev plugin

# Server
build:
	cd server && go build -o ../bin/obsidian-center ./cmd/server

run: build
	./bin/obsidian-center -jwt-secret=dev-secret-change-me -addr=:8080

test:
	cd server && go test ./...

clean:
	rm -rf bin/
	rm -f server/obsidian-center.db

# Plugin
plugin:
	cd plugin && npm run build

plugin-dev:
	cd plugin && npm run dev

# Dev: run server with auto-reload (requires air)
dev:
	cd server && air -c ../.air.toml 2>/dev/null || $(MAKE) run

# Docker
docker-build:
	docker build -t obsidian-center .

docker-run:
	docker run -p 8080:8080 -v oc-data:/data \
		-e OC_JWT_SECRET=change-me-in-production \
		obsidian-center
