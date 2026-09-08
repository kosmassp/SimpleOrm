package migrations

import "embed"

// Snapshots embeds the migrations tree's schema snapshots (ADR-0013 add.3,
// ADR-0017) so rollbacks work deployed — the Go analog of the reference's
// embedded `Migrations/**/*.schema.json` resources
// (CODING-STANDARD §10). Load with orm.SnapshotsFromFS(migrations.Snapshots).
//
//go:embed Table/*/*.schema.json View/*/*.schema.json
var Snapshots embed.FS
