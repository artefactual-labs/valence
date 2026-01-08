# Valence

Valence is an experimental prototype exploring how to evolve a legacy
application by wrapping it in a modern Go facade. It focuses on building new
functionality at the edges through an API-driven approach, rather than
attempting a full rewrite.

This work is driven by a pragmatic constraint: a wholesale replacement is not
currently affordable. Organizational risk, embedded domain knowledge, and the
cost of interrupting incremental delivery make a rewrite impractical in the near
term. Valence explores how to continue delivering value within these
constraints.

## Purpose and audience

This project is intended for maintainers, senior engineers, and contributors
evaluating whether to support or participate in a time-boxed architectural
experiment to extend a legacy system under real delivery pressure. It documents
the motivation, scope, and trade-offs of Valence as an experiment, not as a
mandated architectural direction.

Valence is meant to inform future modernization decisions. It does not commit
the project to FrankenPHP, Go, or this facade model long-term, and its outcomes
should be evaluated independently of any broader rewrite strategy.

## Scope and non-goals

Valence is intentionally limited in scope. It is not a full rewrite, nor an
attempt to migrate the entire legacy application. Only narrow, well-defined
domains are candidates for extraction, and new Go-backed APIs are authoritative
only within their own domain. They must not depend on legacy code for domain
decisions.

The legacy application remains responsible for integration and presentation, not
new business rules. FrankenPHP is treated as an implementation detail of this
experiment rather than a long-term commitment. The experiment should be
reconsidered if it increases cross-system coupling, requires widespread legacy
changes, or cannot be cleanly reversed.

## The core concept

Valence uses [FrankenPHP] to bundle PHP and Go into a single binary. A Go server
acts as a facade, serving the legacy AtoM application while hosting new
functionality. This allows modern code to be delivered without being constrained
by the legacy environment.

This architecture should be understood as transitional. It provides a way to
keep the existing system running while new capabilities are built around it.
Valence makes it possible to deliver functionality without modifying large areas
of legacy code, reducing regression risk and shortening feedback cycles.
Modernization becomes a series of reversible, incremental decisions rather than
a single high-risk rewrite.

## The physical storage experiment

The primary experiment in this repository is new Physical Storage functionality.
It demonstrates how domain logic can be moved to the edges while the legacy
system continues to provide context and presentation.

By hosting coexisting runtimes in a single, easily distributable container,
Valence provides a pragmatic way to stop writing Symfony 1.x code for core
business logic. In this model, a dedicated Go API becomes the authoritative
source for the physical storage domain, while the legacy application acts as UI
and integration glue for archival concepts such as metadata.

```mermaid
graph LR
    Browser --> Valence
    Valence --> Legacy
    Legacy --> Storage API
```

This model introduces meaningful trade-offs. It effectively creates a
distributed system within a single binary, replacing simple local calls with API
boundaries, serialization overhead, and the cognitive cost of managing multiple
runtimes. While this demands higher architectural discipline, it provides a
viable path for replacing legacy components incrementally while preserving a
modern environment for new work.

## A foundation for incremental, self-contained systems

This experiment suggests a broader approach to evolving legacy applications by
hosting multiple coexisting systems within a single runtime. New functionality
is expected to live outside the legacy codebase, with clear ownership and
authority over specific domains.

Importantly, this model supports different styles of iteration. Some changes may
take the form of narrow, modular replacements, such as extracting a single
domain behind an API. Others may be vertical replacements, where an end-to-end
slice of functionality is rebuilt alongside the legacy system. For example, a
read-only version of AtoM could coexist with the legacy application, share the
same database, and be served in parallel, without requiring a coordinated
migration.

Over time, the legacy application shifts toward acting as integration and
presentation glue rather than a place where new business logic accumulates.
Progress is measured by how little legacy code must change to deliver new
features, not by the elimination of PHP itself.

This model is intentionally transitional. Some legacy components may remain
indefinitely, while others are expected to disappear as their responsibilities
move to modern code.

## Want to help?

This is a proof of concept. See [CONTRIBUTING.md] for technical setup and build
instructions.

[FrankenPHP]: https://github.com/php/frankenphp
[CONTRIBUTING.md]: CONTRIBUTING.md
