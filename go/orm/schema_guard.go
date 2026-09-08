package orm

// SchemaGuard is the startup rules (§7.18-§7.21, mirrors
// dotnet/src/SimpleOrm/SchemaGuard.cs and php/src/Validation/SchemaGuard.php):
// [Validate] checks everything a [Registry] declares against the real
// database the session is connected to — every registered query/command,
// every mapped entity, and, when declared, the migration history — without
// executing anything that writes. Every violation is collected into one
// *core.ValidationErrors ([ValidationErrors]); there is no first-error-only
// and no warn-only mode. SchemaGuard lives in this package (not a sibling
// package) so it can reach the session's connection, loader, converter, and
// mapper directly (CODING-STANDARD §1) — the one mapping pipeline
// (db.mapper) is reused rather than built twice.
//
// Go has no field names for a package-level query/command value the way C#
// has Type.Field, so a registry entry's source is its SQLSource.Description()
// — the file path, or a prefix of the inline SQL (CODING-STANDARD §10).

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"regexp"
	"strings"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/mapping"
	"github.com/kosmassp/SimpleOrm/go/orm/internal/metadata"
	"github.com/kosmassp/SimpleOrm/go/orm/migrations"
)

var (
	// selectStarPattern ports the C# reference's lookbehind regex
	// `(?<=select|,)\s*([A-Za-z_]\w*\s*\.\s*)?\*` to Go's RE2, which has no
	// lookbehind: the alternative keeps "select"/"," inside the match instead
	// (CODING-STANDARD §10 — VAL-021 only cares whether it matches).
	selectStarPattern     = regexp.MustCompile(`(?i)(?:select|,)\s*(?:[A-Za-z_]\w*\s*\.\s*)?\*`)
	nonUTCTimestampSQL    = regexp.MustCompile(`(?i)current_timestamp|datetime\s*\(\s*'now'`)
	notNullCommentPattern = regexp.MustCompile(`(?i)--\s*notnull:\s*([^\r\n]+)`)
	stringLiteralPattern  = regexp.MustCompile(`'([^']|'')*'`)
	lineCommentPattern    = regexp.MustCompile(`--[^\r\n]*`)
)

// Validate checks every entry and entity registry declares, plus its
// migration history when registry.Migrations is set, against db's database
// (§7.18). Returns nil when clean, else *core.ValidationErrors carrying every
// violation. Nothing validated leaves a trace: statements are described, never
// executed, inside a shield transaction on the session's connection that is
// always rolled back — TX-001 when the session already has one active.
func Validate(ctx context.Context, db *Db, registry Registry) error {
	return validateAll(ctx, db, registry)
}

// ValidateTypes is the partial form (test harnesses): entries and entities,
// without the migration check (no MIG-030/010/011).
func ValidateTypes(ctx context.Context, db *Db, entries []Entry, entities []reflect.Type) error {
	return validateAll(ctx, db, Registry{Entries: entries, Entities: entities})
}

func validateAll(ctx context.Context, db *Db, registry Registry) error {
	var errs []*core.ValidationError

	if registry.Migrations != nil {
		if err := checkMigrations(ctx, db, registry, &errs); err != nil {
			return err
		}
	}

	tx, err := db.Begin(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback(ctx) }()

	tableColumns := map[string]map[string]migrations.LiveColumn{}

	for _, entry := range registry.Entries {
		source := entry.Source().Description()
		sqlText, err := entry.Source().SQL()
		if err != nil {
			errs = append(errs, &core.ValidationError{Code: core.CodeOf(err), Source: source, Message: err.Error()})
			continue
		}
		if err := validateStatement(
			ctx, db, tableColumns, source, sqlText, entry.ArgsType(), entry.ResultType(), &errs,
		); err != nil {
			return err
		}
	}

	for _, entityType := range registry.Entities {
		if err := validateEntity(ctx, db, tableColumns, entityType, &errs); err != nil {
			return err
		}
	}

	if len(errs) > 0 {
		return &core.ValidationErrors{Errors: errs}
	}
	return nil
}

// --- registry entries and statement-backed entities -----------------------

// validateStatement is the one statement check (§7.19), shared by registry
// entries and statement-backed entities: PRM-001/002 statically (skipped when
// argsType is nil — a statement entity's own parameters are a loader-time
// concern, ADR-0008 add.2, not this check); the VAL-020/021 lints; then
// describe without executing (VAL-001 on failure); for a query
// (resultType != nil) the result shape through the one mapping pipeline
// (MAP-001/002) and, per described column, the VAL-010/011 checks.
func validateStatement(
	ctx context.Context, db *Db, tableColumns map[string]map[string]migrations.LiveColumn,
	source string, sqlText string, argsType, resultType reflect.Type, errs *[]*core.ValidationError,
) error {
	if argsType != nil {
		checkParameters(sqlText, source, argsType, errs)
	}

	stripped := stripLiteralsAndComments(sqlText)
	if selectStarPattern.MatchString(stripped) {
		*errs = append(*errs, &core.ValidationError{
			Code: "VAL-021", Source: source, Message: "SELECT * is not allowed; list columns explicitly",
		})
	}
	if nonUTCTimestampSQL.MatchString(stripped) {
		*errs = append(*errs, &core.ValidationError{
			Code: "VAL-020", Source: source,
			Message: "current_timestamp/datetime('now') store datetimes without a UTC marker; bind an ISO-8601 Z value instead",
		})
	}

	describer, ok := db.options.Dialect.(core.StatementDescriber)
	if !ok {
		*errs = append(*errs, &core.ValidationError{
			Code: "VAL-001", Source: source, Message: fmt.Sprintf("%T cannot describe statements without executing them", db.options.Dialect),
		})
		return nil
	}

	columns, err := describer.DescribeStatement(ctx, db.connection, sqlText)
	if err != nil {
		*errs = append(*errs, &core.ValidationError{Code: "VAL-001", Source: source, Message: err.Error()})
		return nil
	}

	if resultType == nil {
		return nil
	}

	columnNames := make([]string, len(columns))
	for i, column := range columns {
		columnNames[i] = column.Name
	}
	if _, planErr := db.mapper.Plan(resultType, columnNames, source); planErr != nil {
		appendPlanError(errs, source, planErr)
	}

	notNullOverrides := parseNotNullOverrides(sqlText)
	for _, column := range columns {
		member, err := resolveMember(db, resultType, column.Name)
		if err != nil {
			return err
		}
		if member == nil {
			continue // MAP-001 already reported by Plan above.
		}

		if column.Table != "" && column.OriginColumn != "" {
			info, err := getTableColumnInfo(ctx, db, tableColumns, column.Table, column.OriginColumn)
			if err != nil {
				return err
			}
			if info != nil {
				checkColumnAgainstOrigin(source, column.Name, column.Table, *info, member, db, errs)
				continue
			}
		}

		// Expression column: nullability unknowable — require nullable or the override comment.
		if !member.nullable && !notNullOverrides[strings.ToLower(column.Name)] {
			*errs = append(*errs, &core.ValidationError{
				Code: "VAL-010", Source: source,
				Message: fmt.Sprintf(
					"expression column '%s' has unknown nullability; make the member nullable or add '-- notnull: %s'",
					column.Name, column.Name),
			})
		}
	}
	return nil
}

func checkColumnAgainstOrigin(
	source, columnName, table string, info migrations.LiveColumn, member *resolvedMember, db *Db, errs *[]*core.ValidationError,
) {
	if !db.options.Dialect.IsDeclaredTypeCompatible(info.DeclaredType, member.token) && !db.converter.HasHandler(member.goType) {
		*errs = append(*errs, &core.ValidationError{
			Code: "VAL-011", Source: source,
			Message: fmt.Sprintf(
				"column '%s' is declared %s in %s, incompatible with %s (no handler)",
				columnName, info.DeclaredType, table, member.goType),
		})
	}
	if !info.EffectivelyNotNull() && !member.nullable {
		*errs = append(*errs, &core.ValidationError{
			Code: "VAL-010", Source: source,
			Message: fmt.Sprintf("nullable column '%s' maps to non-nullable %s", columnName, member.goType),
		})
	}
}

// checkParameters is PRM-001/002, statically and both directions (§7.13):
// every placeholder in sqlText must name an exported field of argsType, and
// every exported field must be used by some placeholder.
func checkParameters(sqlText, source string, argsType reflect.Type, errs *[]*core.ValidationError) {
	placeholders := core.FindPlaceholders(sqlText)
	properties := publicFieldNames(argsType)

	for _, placeholder := range placeholders {
		if !containsFold(properties, placeholder) {
			*errs = append(*errs, &core.ValidationError{
				Code: "PRM-001", Source: source,
				Message: fmt.Sprintf("SQL parameter @%s has no property on %s", placeholder, argsType.Name()),
			})
		}
	}
	for _, property := range properties {
		if !containsFold(placeholders, property) {
			*errs = append(*errs, &core.ValidationError{
				Code: "PRM-002", Source: source,
				Message: fmt.Sprintf("property %s.%s is never used by the SQL", argsType.Name(), property),
			})
		}
	}
}

// appendPlanError unwraps a mapping pipeline failure into one ValidationError
// per violation: every error inside a *core.MappingErrors (an entity result
// whose own map failed to load), or the single code/message of a plain
// *core.Error (MAP-001/002 from Plan itself).
func appendPlanError(errs *[]*core.ValidationError, source string, err error) {
	var aggregate *core.MappingErrors
	if errors.As(err, &aggregate) {
		for _, e := range aggregate.Errors {
			*errs = append(*errs, &core.ValidationError{Code: e.Code, Source: source, Message: e.Message})
		}
		return
	}
	var single *core.Error
	if errors.As(err, &single) {
		*errs = append(*errs, &core.ValidationError{Code: single.Code, Source: source, Message: single.Message})
		return
	}
	*errs = append(*errs, &core.ValidationError{Code: "MAP-001", Source: source, Message: err.Error()})
}

// resolvedMember is one result column's target, independent of whether
// resultType is an entity, a DTO, or a scalar (§7.19's three shapes).
type resolvedMember struct {
	goType   reflect.Type
	token    core.ColumnType
	nullable bool
}

// resolveMember finds column's target on resultType: the mapped property by
// column name (entity, case-insensitive), the type itself (scalar, using
// mapping.IsScalarType), or the matching exported field (DTO, using
// mapping.NamesMatch) — the same shape mapping.dtoBindings/entityBindings
// resolve, walked again here because Plan (already run above for MAP-001/002)
// does not expose the per-column origin/nullability detail this check needs.
func resolveMember(db *Db, resultType reflect.Type, column string) (*resolvedMember, error) {
	element := resultType
	if element.Kind() == reflect.Pointer {
		element = element.Elem()
	}

	if metadata.HasMappingDeclarations(element) {
		m, err := db.maps.Load(element)
		if err != nil {
			return nil, nil // Already reported via Plan's own load of the same map.
		}
		for _, property := range m.Properties {
			if strings.EqualFold(property.ColumnName, column) {
				return &resolvedMember{goType: property.Type, token: property.ColumnType, nullable: property.IsNullable}, nil
			}
		}
		return nil, nil // MAP-001 already reported by Plan.
	}

	if mapping.IsScalarType(element, db.converter) {
		return &resolvedMember{
			goType: resultType, token: core.ColumnTypeOf(element), nullable: resultType.Kind() == reflect.Pointer,
		}, nil
	}

	for _, field := range reflect.VisibleFields(element) {
		if !field.IsExported() || field.Anonymous {
			continue
		}
		if mapping.NamesMatch(field.Name, column) {
			return &resolvedMember{
				goType: field.Type, token: core.ColumnTypeOf(field.Type), nullable: field.Type.Kind() == reflect.Pointer,
			}, nil
		}
	}
	return nil, nil // MAP-001 already reported by Plan.
}

// --- entities ---------------------------------------------------------------

// validateEntity is the per-entity check (§7.19): a loader failure
// contributes one ValidationError per violation; procedures/materialized
// views the dialect cannot host are skipped (dormant); a statement entity
// validates like a query with a nil args type; tables and views check
// existence and columns, tables additionally declared types and nullability.
func validateEntity(
	ctx context.Context, db *Db, tableColumns map[string]map[string]migrations.LiveColumn,
	entityType reflect.Type, errs *[]*core.ValidationError,
) error {
	source := entityType.Name()
	m, err := db.maps.Load(entityType)
	if err != nil {
		var aggregate *core.MappingErrors
		if errors.As(err, &aggregate) {
			for _, e := range aggregate.Errors {
				*errs = append(*errs, &core.ValidationError{Code: e.Code, Source: source, Message: e.Message})
			}
			return nil
		}
		return err
	}

	switch m.Kind {
	case core.RelationProcedure:
		if !db.options.Dialect.SupportsProcedures() {
			return nil // dormant on this dialect (capability-gated)
		}
	case core.RelationMaterializedView:
		if !db.options.Dialect.SupportsMaterializedViews() {
			return nil // dormant on this dialect (capability-gated)
		}
	}

	if m.Kind == core.RelationStatement {
		return validateStatement(ctx, db, tableColumns, source+" [Statement]", m.DefiningSQL, nil, entityType, errs)
	}

	columns, err := migrations.ReadLiveColumns(ctx, db.connection, db.options.Dialect, m.RelationName)
	if err != nil {
		return err
	}
	if len(columns) == 0 {
		*errs = append(*errs, &core.ValidationError{
			Code: "VAL-012", Source: source, Message: fmt.Sprintf("relation '%s' does not exist in the database", m.RelationName),
		})
		return nil
	}

	for _, property := range m.Properties {
		info, ok := columns[strings.ToLower(property.ColumnName)]
		if !ok {
			*errs = append(*errs, &core.ValidationError{
				Code: "VAL-013", Source: source,
				Message: fmt.Sprintf("mapped column '%s' does not exist in '%s'", property.ColumnName, m.RelationName),
			})
			continue
		}

		if m.Kind != core.RelationTable {
			continue // views report neither declared types nor nullability reliably (§7.19)
		}

		if !db.options.Dialect.IsDeclaredTypeCompatible(info.DeclaredType, property.ColumnType) && !db.converter.HasHandler(property.Type) {
			*errs = append(*errs, &core.ValidationError{
				Code: "VAL-011", Source: source,
				Message: fmt.Sprintf(
					"column '%s' is declared %s, incompatible with %s (no handler)",
					property.ColumnName, info.DeclaredType, property.Target()),
			})
		}
		if !info.EffectivelyNotNull() && !property.IsNullable {
			*errs = append(*errs, &core.ValidationError{
				Code: "VAL-010", Source: source,
				Message: fmt.Sprintf("nullable column '%s' maps to non-nullable %s", property.ColumnName, property.PropertyName),
			})
		}
	}
	return nil
}

// --- migrations (MIG-030/010/011) -------------------------------------------

func checkMigrations(ctx context.Context, db *Db, registry Registry, errs *[]*core.ValidationError) error {
	var snapshots *migrations.SnapshotSet
	if registry.Snapshots != nil {
		set, err := migrations.SnapshotsFromFS(registry.Snapshots)
		if err != nil {
			code := core.CodeOf(err)
			if code == "" {
				return err
			}
			*errs = append(*errs, &core.ValidationError{Code: code, Source: "migrations", Message: err.Error()})
			return nil
		}
		snapshots = set
	}

	runner := migrations.NewRunner(db.pool, db.options.Dialect, db.maps, registry.Migrations, snapshots)
	entries, err := runner.Status(ctx)
	if err != nil {
		code := core.CodeOf(err)
		if code == "" {
			return err
		}
		*errs = append(*errs, &core.ValidationError{Code: code, Source: "migrations", Message: err.Error()})
		return nil
	}

	for _, entry := range entries {
		var code, message string
		switch entry.State {
		case migrations.Pending:
			code, message = "MIG-030", "pending — apply migrations before starting the application"
		case migrations.Drifted:
			code, message = "MIG-010", "applied with a different checksum than the code renders"
		case migrations.Unknown:
			code, message = "MIG-011", "applied in the database but unknown to the code"
		default:
			continue
		}
		*errs = append(*errs, &core.ValidationError{
			Code: code, Source: fmt.Sprintf("V%04d %s", entry.Version, entry.ObjectName), Message: message,
		})
	}
	return nil
}

// --- introspection helpers ---------------------------------------------------

// getTableColumnInfo is table's column, through migrations.ReadLiveColumns
// (the one introspection helper, CODING-STANDARD §8), cached per relation
// across the whole Validate call.
func getTableColumnInfo(
	ctx context.Context, db *Db, cache map[string]map[string]migrations.LiveColumn, table, column string,
) (*migrations.LiveColumn, error) {
	columns, ok := cache[table]
	if !ok {
		var err error
		columns, err = migrations.ReadLiveColumns(ctx, db.connection, db.options.Dialect, table)
		if err != nil {
			return nil, err
		}
		cache[table] = columns
	}
	info, ok := columns[strings.ToLower(column)]
	if !ok {
		return nil, nil
	}
	return &info, nil
}

func publicFieldNames(t reflect.Type) []string {
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	var names []string
	for _, field := range reflect.VisibleFields(t) {
		if field.IsExported() && !field.Anonymous {
			names = append(names, field.Name)
		}
	}
	return names
}

func containsFold(haystack []string, needle string) bool {
	for _, candidate := range haystack {
		if strings.EqualFold(candidate, needle) {
			return true
		}
	}
	return false
}

func stripLiteralsAndComments(sqlText string) string {
	withoutLiterals := stringLiteralPattern.ReplaceAllString(sqlText, "''")
	return lineCommentPattern.ReplaceAllString(withoutLiterals, "")
}

func parseNotNullOverrides(sqlText string) map[string]bool {
	overrides := map[string]bool{}
	for _, match := range notNullCommentPattern.FindAllStringSubmatch(sqlText, -1) {
		for _, name := range strings.Split(match[1], ",") {
			overrides[strings.ToLower(strings.TrimSpace(name))] = true
		}
	}
	return overrides
}
