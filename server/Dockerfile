FROM golang:1.25.14-alpine AS builder

WORKDIR /src
ARG GOPROXY=https://proxy.golang.org,direct
ARG VERSION=dev
ENV GOPROXY=$GOPROXY
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath \
	-ldflags="-s -w -X main.version=${VERSION}" -o /out/agentbox ./cmd/agentbox \
	&& mkdir -p /out/workers \
	&& CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath \
		-ldflags="-s -w -X main.version=${VERSION}" -o /out/workers/agentbox-worker-linux-amd64 ./cmd/agentbox-worker \
	&& CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -trimpath \
		-ldflags="-s -w -X main.version=${VERSION}" -o /out/workers/agentbox-worker-linux-arm64 ./cmd/agentbox-worker \
	&& for asset in /out/workers/agentbox-worker-linux-*; do \
		sha256sum "$asset" | cut -d ' ' -f 1 > "$asset.sha256"; \
	done

FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S agentbox \
    && adduser -S -G agentbox agentbox \
    && mkdir -p /var/lib/agentbox \
    && chown agentbox:agentbox /var/lib/agentbox

WORKDIR /app
COPY --from=builder /out/agentbox /usr/local/bin/agentbox
COPY --from=builder /out/workers /usr/local/lib/agentbox/workers

ENV PORT=8091 \
	AGENTBOX_BIND_HOST=0.0.0.0 \
	AGENTBOX_SECRET_KEY_FILE=/var/lib/agentbox/secret-key \
	AGENTBOX_WORKER_BINARY_DIR=/usr/local/lib/agentbox/workers \
	AGENTBOX_WORKER_CACHE_DIR=/var/lib/agentbox/worker-cache

USER agentbox
EXPOSE 8091
ENTRYPOINT ["/usr/local/bin/agentbox"]
