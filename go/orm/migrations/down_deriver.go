package migrations

import (
	"fmt"
	"strings"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
)

// DeriveDown derives a step's rollback DDL from the versioned snapshots at
// `migrate down` time (ADR-0018): nobody writes Down() — the previous schema
// is recorded, so the reverse is deduced. The step's typed renames are the one
// piece snapshots can't recover (a rename and a drop+add look identical
// between two shapes), so they invert from the step's own declared pairs,
// data-preservingly; everything else — restored columns, dropped columns,
// index changes, a view's previous definition — comes from the snapshot diff.
// Down() remains the manual override (resolved by the caller before this is
// reached); a change the snapshots can't express (type/nullability) refuses
// with MIG-020.
//
// The return is nil, nil when snapshots holds nothing at (objectName,
// version): the caller then refuses with MIG-020, naming the step.
func DeriveDown(
	objectName string, version int64, snapshots *SnapshotSet,
	upRenames []ColumnRename, notices *[]string, dialect core.Dialect,
) ([]MigrationStatement, error) {
	if snapshots == nil {
		return nil, nil
	}
	at := snapshots.At(objectName, version)
	if at == nil {
		return nil, nil
	}
	before := snapshots.LatestBefore(objectName, version)

	if at.Table == nil {
		var beforeDDL *string
		if before != nil {
			beforeDDL = &before.DDL
		}
		return deriveViewDown(objectName, at.DDL, beforeDDL), nil
	}

	var beforeTable *TableSchema
	if before != nil {
		beforeTable = before.Table
	}
	return deriveTableDown(objectName, version, at.Table, beforeTable, upRenames, notices, dialect)
}

// deriveViewDown mirrors the reference's guarded drop + restore: the apply
// guard in reverse (MIG-012) protects an outside hotfix from a rollback too.
func deriveViewDown(objectName, ddlAt string, ddlBefore *string) []MigrationStatement {
	statements := []MigrationStatement{
		{SQL: ddlAt, Origin: "derived expect " + objectName, GuardView: objectName},
		{SQL: "drop view if exists " + objectName, Origin: "derived drop " + objectName},
	}
	if ddlBefore != nil {
		statements = append(statements, MigrationStatement{SQL: *ddlBefore, Origin: "derived restore " + objectName})
	}
	return statements
}

// namedColumn pairs a snapshot column with the display name a message or a
// rendered statement should use (the "before" name for a renamed column).
type namedColumn struct {
	displayName string
	column      TableColumn
}

func deriveTableDown(
	objectName string, version int64, at *TableSchema, before *TableSchema,
	upRenames []ColumnRename, notices *[]string, dialect core.Dialect,
) ([]MigrationStatement, error) {
	if before == nil {
		return []MigrationStatement{{SQL: dialect.DropTableSQL(objectName), Origin: "derived drop " + objectName}}, nil
	}

	// A non-nil (possibly empty) slice, always: nil is DeriveDown's own "no
	// snapshot to derive from" sentinel (checked by the caller), so a
	// data-only step whose schema didn't change here must still come back as
	// a real, empty slice — a derived, schema-correct no-op rollback, never MIG-020.
	statements := []MigrationStatement{}

	// Renames invert first (rename -> add -> remove ordering, §7.22), reverse
	// declaration order, mapping the current names back to their "before" ones.
	nameAtToBefore := map[string]string{}
	for i := len(upRenames) - 1; i >= 0; i-- {
		rename := upRenames[i]
		statements = append(statements, MigrationStatement{
			SQL: dialect.RenameColumnSQL(objectName, rename.To, rename.From), Origin: "derived rename " + objectName,
		})
		nameAtToBefore[strings.ToLower(rename.To)] = rename.From
	}

	current := map[string]namedColumn{}
	for _, c := range at.Columns {
		display := c.Name
		key := strings.ToLower(c.Name)
		if renamed, ok := nameAtToBefore[key]; ok {
			display = renamed
			key = strings.ToLower(renamed)
		}
		current[key] = namedColumn{displayName: display, column: c}
	}
	previous := map[string]namedColumn{}
	for _, c := range before.Columns {
		previous[strings.ToLower(c.Name)] = namedColumn{displayName: c.Name, column: c}
	}

	// Dropped columns restore nullable — the database refuses a bare NOT NULL
	// addition; the constraint (and the data) are not derivable.
	for _, c := range before.Columns {
		key := strings.ToLower(c.Name)
		if _, exists := current[key]; exists {
			continue
		}
		statements = append(statements, MigrationStatement{
			SQL:    dialect.AddColumnSQL(objectName, c.Name, c.StorageType, true, ""),
			Origin: "derived add " + objectName,
		})
		if !c.Nullable {
			*notices = append(*notices, fmt.Sprintf(
				"V%04d %s.%s: restored nullable — the NOT NULL constraint (and the data) are not derivable",
				version, objectName, c.Name))
		}
	}

	// Added columns are dropped. Keys carry the renamed-back names — by the
	// time these run, the reverse renames above have already been applied.
	for _, c := range at.Columns {
		key := strings.ToLower(c.Name)
		if renamed, ok := nameAtToBefore[key]; ok {
			key = strings.ToLower(renamed)
		}
		if _, existed := previous[key]; existed {
			continue
		}
		statements = append(statements, MigrationStatement{
			SQL: dialect.DropColumnSQL(objectName, current[key].column.Name), Origin: "derived remove " + objectName,
		})
	}

	// A same-column type/nullability change is not derivable. Iterate at.Columns
	// (translated) rather than the map for a deterministic first-mismatch report.
	for _, c := range at.Columns {
		key := strings.ToLower(c.Name)
		if renamed, ok := nameAtToBefore[key]; ok {
			key = strings.ToLower(renamed)
		}
		now, then := current[key], previous[key]
		if _, existed := previous[key]; !existed {
			continue
		}
		if !strings.EqualFold(now.column.StorageType, then.column.StorageType) || now.column.Nullable != then.column.Nullable {
			return nil, core.NewError("MIG-020", fmt.Sprintf("V%04d %s", version, objectName),
				fmt.Sprintf("column %s changed type/nullability at this version; that rollback cannot be derived — override Down()", now.displayName))
		}
	}

	// Indexes match structurally (ADR-0017 add.2) here too.
	applyRenames := func(signature string) string {
		if len(nameAtToBefore) == 0 {
			return signature
		}
		parts := strings.SplitN(signature, "|", 2)
		if len(parts) != 2 {
			return signature
		}
		columns := strings.Split(parts[1], ",")
		for i, part := range columns {
			descending := strings.HasSuffix(part, " desc")
			column := part
			if descending {
				column = strings.TrimSuffix(part, " desc")
			}
			if renamed, ok := nameAtToBefore[column]; ok {
				column = strings.ToLower(renamed)
			}
			if descending {
				columns[i] = column + " desc"
			} else {
				columns[i] = column
			}
		}
		return parts[0] + "|" + strings.Join(columns, ",")
	}

	for _, index := range at.Indexes {
		signature := applyRenames(IndexSignature(index))
		matched := false
		for _, beforeIndex := range before.Indexes {
			if IndexSignature(beforeIndex) == signature {
				matched = true
				break
			}
		}
		if !matched {
			statements = append(statements, MigrationStatement{
				SQL: dialect.DropIndexSQL(objectName, index.Name), Origin: "derived drop index",
			})
		}
	}
	for _, index := range before.Indexes {
		signature := IndexSignature(index)
		matched := false
		for _, atIndex := range at.Indexes {
			if applyRenames(IndexSignature(atIndex)) == signature {
				matched = true
				break
			}
		}
		if !matched {
			statements = append(statements, MigrationStatement{
				SQL: CreateIndexSQL(objectName, index), Origin: "derived add index",
			})
		}
	}

	return statements, nil
}
