FROM node:20-alpine AS web-builder

WORKDIR /src/web
COPY web/package.json web/package-lock.json ./
RUN npm ci
COPY web/ ./
RUN npm run build

FROM golang:1.22-alpine AS server-builder

RUN apk add --no-cache gcc musl-dev

WORKDIR /src
COPY server/ ./server/

WORKDIR /src/server
RUN go mod download
RUN CGO_ENABLED=1 go build -o /obsidian-center ./cmd/server

FROM alpine:3.19

RUN apk add --no-cache ca-certificates

COPY --from=server-builder /obsidian-center /usr/local/bin/obsidian-center
COPY --from=web-builder /src/web/index.html /web/index.html
COPY --from=web-builder /src/web/style.css /web/style.css
COPY --from=web-builder /src/web/dist/ /web/dist/

RUN mkdir -p /data

EXPOSE 8080

HEALTHCHECK --interval=30s --timeout=5s --start-period=5s --retries=3 \
    CMD wget -q --spider http://localhost:8080/api/health || exit 1

ENTRYPOINT ["obsidian-center"]
CMD ["-addr=:8080", "-db=/data/obsidian-center.db", "-web-dir=/web"]
