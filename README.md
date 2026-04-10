# Kervan Server

Kervan is a unified file transfer server skeleton that supports:

- FTP core (authentication, navigation, listing, upload/download, passive data channel)
- FTPS core (`AUTH TLS`, `PBSZ`, `PROT`, optional implicit FTPS listener)
- SSH transport with SFTP core + SCP source/sink handling
- Local and memory backends through a virtual filesystem layer
- Config system with defaults, validation, env overlay, and reload support
- Local user/auth store with Argon2id and bcrypt password hashing
- Session tracking and JSONL audit logging
- Transfer tracking with API exposure and Prometheus-style `/metrics`
- REST API for auth, users, sessions, transfers, files, server status, and audit events

## Requirements

- Go 1.26.2 (toolchain is pinned in `go.mod`)

## Quick Start

```bash
go run ./cmd/kervan init
go run ./cmd/kervan
```

## Useful Commands

```bash
go run ./cmd/kervan version
go run ./cmd/kervan keygen --type ed25519 --output ./data/host_keys
go run ./cmd/kervan admin create --username admin --password 'StrongPass123!'
go run ./cmd/kervan admin reset-password --username admin --password 'NewStrongPass123!'
```

## Build & Test

```bash
go test ./...
go build ./cmd/kervan
```

## FTPS Notes

- Set `ftps.enabled: true` and provide both `ftps.cert_file` and `ftps.key_file`.
- Choose `ftps.mode` as `explicit`, `implicit`, or `both`.
- Implicit mode listens on `ftps.implicit_port` (default `990`).
