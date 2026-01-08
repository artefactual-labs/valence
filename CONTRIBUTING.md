# Contributing

This document captures build, run, and developer workflow details.

## Quick start

Check out the repository and initialize the AtoM submodule:

    git submodule update --init --recursive

Build the services:

    docker compose build

Run the backend services in the background:

    docker compose up --detach elasticsearch percona memcached gearmand

Run the Valence service in the foreground:

    docker compose up valence

Ready! Now you can open <http://127.0.0.1:14800/> from your browser.

### The experiment

Valence acts as a facade in front of the legacy AtoM (Symfony 1.x) application.
A Go server receives incoming requests and decides whether to handle them
directly or pass them to AtoM. The new Storage feature shows how this setup
works in practice.

Inside this action, the PHP code makes an internal HTTP request back to Valence
using a `ValenceStorageClient` built on cURL. This request targets the Storage
API, where the Go code in `storage.go` retrieves and filters the data, currently
from a seed list, and returns it as JSON. The PHP action receives this data,
assigns it to the `$locations` variable, and renders the final HTML using the
standard AtoM template `listSuccess.php`.

### The container image

Valence produces a straightforward Linux container image. At its core is a
single Go binary that bundles together several components: the Valence server
itself, the new Storage API, the legacy AtoM application, and a statically
compiled PHP interpreter. This "batteries included" approach means the container
ships as a self-contained unit — no external PHP runtime, no separate process
managers, and minimal runtime dependencies beyond the OS base.

The build process is a multi-stage Dockerfile. First, the AtoM frontend assets
are compiled via Node.js, and the PHP application is prepared with its Composer
dependencies. Then, a static PHP embed library is compiled using
[static-php-cli] on a glibc-based builder. This static library is linked into
the Go binary at compile time, allowing the Go server to execute PHP code
in-process via [FrankenPHP]. The final runtime image is based on Debian
bookworm-slim and contains only the Go binary and the pre-built AtoM application
files—nothing else.

This design keeps the operational footprint minimal. There's one process to
manage, one binary to deploy, and one image to distribute. The Go server can
handle requests natively (like the Storage API) or delegate them to the embedded
PHP runtime for legacy functionality. It's a practical way to modernize
incrementally without abandoning the existing codebase or introducing the
complexity of running multiple services.

[static-php-cli]: https://static-php.dev
[FrankenPHP]: https://frankenphp.dev

### What's missing

Currently, Valence does not know how to run AtoM background workers (Gearman)
nor does it provide direct access to the legacy Symfony CLI (e.g. `php
symfony`). However, both of these are solvable problems.
