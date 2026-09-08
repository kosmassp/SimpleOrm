package metadata

import (
	"strings"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
)

// Export renders m as the conformance JSON of spec/metadata-model.md — the
// exact port of EntityMapJson.cs, using core.CanonicalJSON so the byte output
// matches the C# reference's System.Text.Json encoding. Column-centric and
// language-neutral: column names, neutral type tokens, and SQL-side parameter
// names only — never Go field names. maps resolves the related entities whose
// columns a relationship names (ADR-0029: every foreign-key reference exports
// by column); loading one of them is the only way Export can fail.
func Export(m *core.EntityMap, maps *Loader) (string, error) {
	root := core.JSONObject{}
	root = root.Set("entity", m.EntityName())
	root = root.Set("source", buildSource(m))
	root = root.Set("key", buildKey(m))

	if m.VersionProperty != nil {
		root = root.Set("version", m.VersionProperty.ColumnName)
	}

	root = root.Set("columns", buildColumns(m))

	if len(m.Indexes) > 0 {
		root = root.Set("indexes", buildIndexes(m))
	}

	if len(m.Relationships) > 0 {
		relationships, err := buildRelationships(m, maps)
		if err != nil {
			return "", err
		}
		root = root.Set("relationships", relationships)
	}

	return core.CanonicalJSON(root), nil
}

func buildSource(m *core.EntityMap) core.JSONObject {
	source := core.JSONObject{}
	source = source.Set("kind", m.Kind.Token())

	if m.Kind == core.RelationStatement {
		source = source.Set("sql", normalizeSQL(m.DefiningSQL))
		source = source.Set("parameters", buildParameters(m.StatementParameters))
		return source
	}

	source = source.Set("name", m.RelationName)
	if m.Schema != "" {
		source = source.Set("schema", m.Schema)
	}
	if m.DefiningSQL != "" {
		source = source.Set("sql", normalizeSQL(m.DefiningSQL))
	}
	if m.Kind == core.RelationProcedure {
		source = source.Set("parameters", buildParameters(m.StatementParameters))
	}
	return source
}

func buildKey(m *core.EntityMap) core.JSONObject {
	key := core.JSONObject{}
	key = key.Set("strategy", m.KeyStrategy.Token())
	columns := make([]any, len(m.KeyProperties))
	for i, k := range m.KeyProperties {
		columns[i] = k.ColumnName
	}
	key = key.Set("columns", columns)
	return key
}

func buildParameters(params []core.StatementParameter) []any {
	result := make([]any, len(params))
	for i, p := range params {
		obj := core.JSONObject{}
		obj = obj.Set("name", p.Name)
		obj = obj.Set("type", core.TypeToken(p.Type, core.ColumnTypeOf(p.Type)))
		result[i] = obj
	}
	return result
}

func buildColumns(m *core.EntityMap) []any {
	result := make([]any, len(m.Properties))
	for i, p := range m.Properties {
		obj := core.JSONObject{}
		obj = obj.Set("column", p.ColumnName)
		obj = obj.Set("type", core.TypeToken(p.Type, p.ColumnType))
		obj = obj.Set("nullable", p.IsNullable)
		if p.IsKey {
			obj = obj.Set("key", true)
		}
		if p.IsGenerated {
			obj = obj.Set("generated", true)
		}
		result[i] = obj
	}
	return result
}

func buildIndexes(m *core.EntityMap) []any {
	result := make([]any, len(m.Indexes))
	for i, index := range m.Indexes {
		obj := core.JSONObject{}
		obj = obj.Set("name", index.Name)
		columns := make([]any, len(index.Columns))
		for j, c := range index.Columns {
			column := core.JSONObject{}
			column = column.Set("column", c.ColumnName)
			direction := "asc"
			if c.Descending {
				direction = "desc"
			}
			column = column.Set("direction", direction)
			columns[j] = column
		}
		obj = obj.Set("columns", columns)
		if index.Unique {
			obj = obj.Set("unique", true)
		}
		result[i] = obj
	}
	return result
}

func buildRelationships(m *core.EntityMap, maps *Loader) ([]any, error) {
	result := make([]any, len(m.Relationships))
	for i, r := range m.Relationships {
		obj := core.JSONObject{}
		switch r.Kind {
		case core.RelationshipManyToOne:
			obj = obj.Set("kind", "many_to_one")
			obj = obj.Set("foreignKeyColumns", foreignKeyColumns(m, r.ForeignKeyProperties))
			obj = obj.Set("references", r.TargetType.Name())

		case core.RelationshipOneToMany, core.RelationshipOneToOne:
			// The FKs live on the target; resolve their columns through the
			// target's own map (ADR-0029: column names everywhere).
			kindToken := "one_to_many"
			if r.Kind == core.RelationshipOneToOne {
				kindToken = "one_to_one"
			}
			targetMap, err := maps.Load(r.TargetType)
			if err != nil {
				return nil, err
			}
			obj = obj.Set("kind", kindToken)
			obj = obj.Set("references", r.TargetType.Name())
			obj = obj.Set("targetForeignKeyColumns", foreignKeyColumns(targetMap, r.ForeignKeyProperties))

		default: // core.RelationshipManyToMany
			linkMap, err := maps.Load(r.LinkType)
			if err != nil {
				return nil, err
			}
			obj = obj.Set("kind", "many_to_many")
			obj = obj.Set("references", r.TargetType.Name())
			obj = obj.Set("through", r.LinkType.Name())
			obj = obj.Set("linkForeignKeyColumnsToOwner", foreignKeyColumns(linkMap, r.LinkForeignKeysToOwner))
			obj = obj.Set("linkForeignKeyColumnsToTarget", foreignKeyColumns(linkMap, r.LinkForeignKeysToTarget))
		}
		result[i] = obj
	}
	return result, nil
}

// foreignKeyColumns resolves FK property names to their column names on the
// map that owns them — column names are the language-neutral contract
// (spec/metadata-model.md, ADR-0029).
func foreignKeyColumns(owner *core.EntityMap, propertyNames []string) []any {
	result := make([]any, len(propertyNames))
	for i, name := range propertyNames {
		if p := owner.Property(name); p != nil {
			result[i] = p.ColumnName
		}
	}
	return result
}

// normalizeSQL collapses whitespace so exported SQL is layout-independent across implementations.
func normalizeSQL(sql string) string {
	return strings.Join(strings.Fields(sql), " ")
}
