# SimpleOrm

A SQL-first micro-ORM for .NET, PostgreSQL first. The database is the source of truth;
real SQL is first-class; no hidden queries; strict by default; async only.

This repository is a monorepo: the C# implementation is the **reference**; a
language-neutral [spec](spec/) and a [conformance suite](conformance/) grow alongside
it so the library can later be ported (Go, Java, PHP) and extended to other dialects.

**Status: Level 1, milestone 1 (skeleton).** See [CLAUDE.md](CLAUDE.md) for the full
project brief and [docs/decisions.md](docs/decisions.md) for the decision log.

## Layout

```
spec/          language-neutral spec (grows per milestone)
conformance/   executable definition: JSON cases every implementation must pass
dotnet/        C# reference implementation
  src/SimpleOrm/           core (netstandard2.0 + net10.0, depends only on System.Data.Common)
  src/SimpleOrm.Postgres/  Postgres dialect (Npgsql; conditional per-TFM references)
  src/SimpleOrm.Cli/       CLI (stub until milestone 5)
  tests/SimpleOrm.Tests/   integration tests against real PostgreSQL
docs/decisions.md          ADR log
```

## Building and testing

Requires the .NET 10 SDK and a reachable PostgreSQL server (no Docker required —
see ADR-0002).

```
dotnet build dotnet/SimpleOrm.sln
dotnet test dotnet/SimpleOrm.sln
```

Tests read the `ORM_TEST_CONNECTION` environment variable
(default: `Host=localhost;Port=5432;Username=postgres;Database=simpleorm_test`)
and create the `simpleorm_test` database automatically if it does not exist.
