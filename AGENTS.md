# Agent Development Guide

A file for [guiding coding agents](https://agents.md/).

## Commands

- **Build (local Go):** `go build -o bin/valence ./cmd/valence`
- **Build (Docker):** `make build`
- **Dev image + run:** `make dev`
- **Formatting (Go):** `gofmt -w cmd internal`

## Directory structure

- Go entrypoint: `cmd/valence`
- Go support packages: `internal/`
- Legacy AtoM submodule: `atom/`
- Docker build: `Dockerfile`

## Project architecture

Valence is a Go HTTP facade that embeds FrankenPHP to run the legacy AtoM
Symfony 1.x app. Its core goal is to make room for new capabilities to coexist
with the legacy application inside a single container. The Go server owns
routing, static asset handling, and native endpoints, while all other requests
are forwarded to the AtoM front controller. At startup, Valence bootstraps
legacy config files from env, waits for dependencies, runs `tools:purge --demo`,
clears cache, and then starts the FrankenPHP-backed server.

Key responsibilities:

- **Bootstrap**: generate AtoM config files, ini drop-in, and `/sf` symlink.
- **Routing**: serve static assets directly, block internal paths, and forward
  to the Symfony front controller.
- **CLI tasks**: run `tools:purge --demo` and `symfony cc` via FrankenPHP CLI.
- **Dependencies**: wait for MySQL and Elasticsearch before boot.
