# syntax=docker/dockerfile:1.7

ARG GO_VERSION=1.26.2
ARG NODE_VERSION=22-alpine
ARG ALPINE_VERSION=3.22

FROM node:${NODE_VERSION} AS webui-build
WORKDIR /src/webui

COPY webui/package.json webui/package-lock.json ./
RUN npm ci

COPY webui/ ./
RUN npm run build

FROM golang:${GO_VERSION}-alpine AS go-build
WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
COPY --from=webui-build /src/webui/dist ./internal/webui/dist

ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG VERSION=dev
ARG COMMIT=unknown
ARG DATE=unknown

RUN CGO_ENABLED=0 GOOS=${TARGETOS} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags "-s -w \
    -X github.com/kervanserver/kervan/internal/build.Version=${VERSION} \
    -X github.com/kervanserver/kervan/internal/build.Commit=${COMMIT} \
    -X github.com/kervanserver/kervan/internal/build.Date=${DATE}" \
    -o /out/kervan ./cmd/kervan

FROM alpine:${ALPINE_VERSION}

RUN apk add --no-cache ca-certificates tzdata
RUN addgroup -S -g 10001 kervan && adduser -S -D -H -u 10001 -G kervan kervan

WORKDIR /var/lib/kervan

COPY --from=go-build /out/kervan /usr/local/bin/kervan
COPY --chmod=755 scripts/docker-entrypoint.sh /usr/local/bin/docker-entrypoint.sh
COPY kervan.example.yaml /var/lib/kervan/kervan.yaml

RUN mkdir -p /var/lib/kervan/data/files /var/lib/kervan/data/host_keys && \
    chown -R kervan:kervan /var/lib/kervan

ENV KERVAN_CONFIG=/var/lib/kervan/kervan.yaml

VOLUME ["/var/lib/kervan/data"]

EXPOSE 2121 990 2222 8080 50000-50100

HEALTHCHECK --interval=30s --timeout=5s --start-period=20s --retries=3 \
  CMD wget -qO- http://127.0.0.1:8080/health >/dev/null || exit 1

USER kervan:kervan

ENTRYPOINT ["/usr/local/bin/docker-entrypoint.sh"]
CMD []
