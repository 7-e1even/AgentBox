FROM golang:1.25-alpine AS builder

WORKDIR /src
ARG GOPROXY=https://proxy.golang.org,direct
ENV GOPROXY=$GOPROXY
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags="-s -w" -o /out/agentbox ./cmd/agentbox

FROM alpine:3.22

RUN apk add --no-cache ca-certificates tzdata \
    && addgroup -S agentbox \
    && adduser -S -G agentbox agentbox \
    && mkdir -p /var/lib/agentbox \
    && chown agentbox:agentbox /var/lib/agentbox

WORKDIR /app
COPY --from=builder /out/agentbox /usr/local/bin/agentbox

ENV PORT=8091 \
    AGENTBOX_BIND_HOST=0.0.0.0 \
    AGENTBOX_SECRET_KEY_FILE=/var/lib/agentbox/secret-key

USER agentbox
EXPOSE 8091
ENTRYPOINT ["/usr/local/bin/agentbox"]
