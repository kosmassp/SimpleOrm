package migrations

import (
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
)

// EmitTriggerStep emits one trigger step's Go source (Level 4, ADR-0042; destined for
// Trigger/<TriggerClass>/V000N_<Description>.go) from a **literal declaration** — never metadata-rendered, so
// the applied step's checksum can never drift when the declaration changes again. A new trigger is a Recreate
// of the declaration (drop if present, create); a change opens with ExpectDefinition of the previous
// declaration (MIG-012 on outside drift) and then recreates from the new one; a removal (current nil) is a
// bare DropTrigger. The step embeds orm.TriggerMigration[Entity, TriggerClass], so both types must still be in
// the program (keep them until a removal has applied).
func EmitTriggerStep(
	entityType, triggerType reflect.Type, version int64, description string, current, previous *TriggerDeclaration,
) (string, error) {
	entityImportPath, triggerImportPath := entityType.PkgPath(), triggerType.PkgPath()
	entityAlias, triggerAlias := packageAlias(entityImportPath), packageAlias(triggerImportPath)
	typeName := fmt.Sprintf("V%04d_%s", version, description)
	pkgName := strings.ToLower(triggerType.Name())

	var body strings.Builder
	if current == nil {
		body.WriteString("\tactions.DropTrigger()\n")
	} else {
		if previous != nil {
			fmt.Fprintf(&body, "\tactions.ExpectDefinition(%s)\n", triggerLiteralArguments(previous))
		}
		fmt.Fprintf(&body, "\tactions.Recreate(%s)\n", triggerLiteralArguments(current))
	}

	imports := importBlock(map[string]string{entityAlias: entityImportPath, triggerAlias: triggerImportPath, "orm": ormImportPath})
	source := fmt.Sprintf(
		"// %s (ADR-0042): the literal declaration, guarded by the expected previous one\n"+
			"// (MIG-012 on outside drift); the rollback derives from the snapshots (ADR-0018).\n"+
			"package %s\n\nimport (\n%s)\n\ntype %s struct {\n\torm.TriggerMigration[%s.%s, %s.%s]\n}\n\n"+
			"func (%s) Action(actions *orm.TriggerActions) {\n%s}\n",
		GeneratedMarker, pkgName, imports, typeName, entityAlias, entityType.Name(), triggerAlias, triggerType.Name(),
		typeName, body.String())
	return formatGoSource(source)
}

// triggerLiteralArguments is a declaration as the arguments of Recreate and ExpectDefinition: timing, events,
// the body (as the author wrote it for a model declaration, as normalized for one read from a snapshot),
// then the update-of columns.
func triggerLiteralArguments(d *TriggerDeclaration) string {
	timings := map[core.TriggerTiming]string{
		core.TimingBefore: "orm.TimingBefore", core.TimingAfter: "orm.TimingAfter", core.TimingInsteadOf: "orm.TimingInsteadOf",
	}
	events := make([]string, 0, 3)
	for _, event := range d.Events.List() {
		token := event.Token()
		events = append(events, "orm.Event"+strings.ToUpper(token[:1])+token[1:])
	}
	arguments := []string{timings[d.Timing], strings.Join(events, " | "), goStringLiteral(strings.TrimSpace(d.SQL))}
	for _, column := range d.UpdateOf {
		arguments = append(arguments, strconv.Quote(column))
	}
	return strings.Join(arguments, ", ")
}
