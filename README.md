# SimpleOrm

A SQL-first micro-ORM for .NET, SQLite first. The database is the source of truth;
real SQL is first-class; no hidden queries; strict by default; async only.

This repository is a monorepo: the C# implementation is the **reference**; a
language-neutral [spec](spec/) and a [conformance suite](conformance/) grow alongside
it so the library can later be ported (Go, Java, PHP) and extended to other dialects
(PostgreSQL, MySQL, SQL Server).

**Status: Level 1, milestone 1 (skeleton).** See [CLAUDE.md](CLAUDE.md) for the full
project brief and [docs/decisions.md](docs/decisions.md) for the decision log
(ADR-0003: SQLite replaced PostgreSQL as the reference database).

## Layout

```
spec/          language-neutral spec (grows per milestone)
conformance/   executable definition: JSON cases every implementation must pass
dotnet/        C# reference implementation
  src/SimpleOrm/           core (netstandard2.0 + net10.0, depends only on System.Data.Common)
  src/SimpleOrm.Sqlite/    SQLite dialect (Microsoft.Data.Sqlite)
  src/SimpleOrm.Cli/       CLI (stub until milestone 5)
  tests/SimpleOrm.Tests/   integration tests against real SQLite databases
docs/decisions.md          ADR log
```

## Building and testing

Requires only the .NET 10 SDK — no database server, no Docker. Tests create a
temp-file SQLite database per fixture and delete it afterwards; the native SQLite
library ships with `Microsoft.Data.Sqlite`.

```
dotnet build dotnet/SimpleOrm.sln
dotnet test dotnet/SimpleOrm.sln
```
