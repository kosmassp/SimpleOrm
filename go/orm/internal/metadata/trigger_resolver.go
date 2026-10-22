package metadata

import (
	"reflect"
	"sort"
	"strings"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
)

// resolveTriggers turns an entity's trigger links into its map's resolved triggers (Level 4, ADR-0042,
// spec/metadata-model.md "Triggers"), in link order, collecting every MAP-029 violation beside the entity's
// other errors — never first-error-only. The Go declaration is typed, so two of the spec's sub-cases cannot
// occur and are recorded here instead of checked (CODING-STANDARD §10): a class "with two declarations" (a
// type has one Trigger method) and a body written in a language the compiler would reject.
//
// A link is checked for: the source (a table takes every timing, a view only instead-of, no other source
// takes a trigger), the class (it must declare a trigger and be neither an entity nor an owned type), the
// declaration (a name, a timing, at least one event, a body), the update-of list (only with the update
// event, naming mapped properties — resolved to columns), and the body's tokens (known and well formed,
// naming mapped columns, possible for the events), and a class linked twice by one entity.
func resolveTriggers(
	entityType reflect.Type, kind core.RelationKind, links []core.TriggerLink,
	properties []*core.PropertyMap, byProperty map[string]*core.PropertyMap, errs *errorCollector,
) []*core.EntityTrigger {
	if len(links) == 0 {
		return nil
	}

	columns := map[string]bool{}
	for _, p := range properties {
		columns[p.ColumnName] = true
	}

	var triggers []*core.EntityTrigger
	linked := map[reflect.Type]bool{}
	for _, link := range links {
		if link.Type == nil {
			errs.add("MAP-029", entityType.Name(), "a trigger link names no class; write orm.Trigger[YourTrigger]()")
			continue
		}
		t := core.TypeName(link.Type)
		name := target(entityType, link.Type.Name())

		if kind != core.RelationTable && kind != core.RelationView {
			errs.add("MAP-029", name, "a trigger link is only valid on a table or a view (instead-of), not a %s", kind)
			continue
		}
		if linked[link.Type] {
			errs.add("MAP-029", name, "the trigger class %s is linked twice by this entity", t)
			continue
		}
		linked[link.Type] = true

		if definition, ok := resolveTrigger(entityType, kind, link, t, name, columns, byProperty, errs); ok {
			triggers = append(triggers, &core.EntityTrigger{Type: link.Type, Definition: definition})
		}
	}
	return triggers
}

// resolveTrigger checks and resolves one link; false when any rule failed (every failure reported).
func resolveTrigger(
	entityType reflect.Type, kind core.RelationKind, link core.TriggerLink, typeName, name string,
	columns map[string]bool, byProperty map[string]*core.PropertyMap, errs *errorCollector,
) (core.TriggerDefinition, bool) {
	before := len(errs.errors)

	declaration, declared := core.TriggerDeclarationOf(link.Type)
	if !declared {
		errs.add("MAP-029", name, "%s carries no trigger declaration; give it a Trigger() orm.TriggerDef method", typeName)
		return core.TriggerDefinition{}, false
	}
	if HasMappingDeclarations(link.Type) || core.IsOwnedType(link.Type) {
		errs.add("MAP-029", name, "%s is an entity or an owned type; a trigger class is a declaration object with no columns and no key", typeName)
	}

	if strings.TrimSpace(declaration.Name) == "" {
		errs.add("MAP-029", name, "the trigger declares a blank name")
	}
	if declaration.Timing.Token() == "" {
		errs.add("MAP-029", name, "the trigger declares no timing (orm.TimingBefore, TimingAfter, or TimingInsteadOf)")
	}
	if declaration.Events == 0 || declaration.Events&^(core.EventInsert|core.EventUpdate|core.EventDelete) != 0 {
		errs.add("MAP-029", name, "the trigger declares no valid event (orm.EventInsert, EventUpdate, EventDelete, combined with |)")
	}
	if strings.TrimSpace(declaration.SQL) == "" {
		errs.add("MAP-029", name, "the trigger declares a blank body")
	}
	if kind == core.RelationView && declaration.Timing != core.TimingInsteadOf && declaration.Timing.Token() != "" {
		errs.add("MAP-029", name, "a view takes only instead_of triggers, not %s", declaration.Timing.Token())
	}

	updateOf := resolveUpdateOf(declaration, name, byProperty, errs)

	tokens, problems := core.ScanTriggerTokens(declaration.SQL)
	for _, problem := range problems {
		errs.add("MAP-029", name, "%s", problem)
	}
	// The body is shared by every declared event, so a row token must have rows on each of them: {new} refuses
	// a declaration that includes delete, {old} one that includes insert.
	for _, token := range tokens {
		switch token.Kind {
		case core.TokenNew:
			if declaration.Events.Has(core.EventDelete) {
				errs.add("MAP-029", name, "%s has no rows on the delete event; a body is shared by every declared event", token.Canonical())
			}
		case core.TokenOld:
			if declaration.Events.Has(core.EventInsert) {
				errs.add("MAP-029", name, "%s has no rows on the insert event; a body is shared by every declared event", token.Canonical())
			}
		}
		for _, column := range token.Columns {
			if !columns[column] {
				errs.add("MAP-029", name, "%s names '%s', which is not a column of %s", token.Canonical(), column, entityType.Name())
			}
		}
	}

	if len(errs.errors) > before {
		return core.TriggerDefinition{}, false
	}
	return core.TriggerDefinition{
		Name:     strings.TrimSpace(declaration.Name),
		Timing:   declaration.Timing,
		Events:   declaration.Events,
		UpdateOf: updateOf,
		SQL:      strings.TrimSpace(declaration.SQL),
	}, true
}

// resolveUpdateOf maps the declared update-of property names to column names: valid only with the update
// event, every name a mapped property (an owned member by its dotted path).
func resolveUpdateOf(declaration core.TriggerDef, name string, byProperty map[string]*core.PropertyMap, errs *errorCollector) []string {
	if len(declaration.UpdateOf) == 0 {
		return nil
	}
	if !declaration.Events.Has(core.EventUpdate) {
		errs.add("MAP-029", name, "UpdateOf is only valid with the update event")
	}
	resolved := make([]string, 0, len(declaration.UpdateOf))
	for _, property := range declaration.UpdateOf {
		mapped, ok := byProperty[property]
		if !ok {
			errs.add("MAP-029", name, "UpdateOf names '%s', which is not a mapped property", property)
			continue
		}
		resolved = append(resolved, mapped.ColumnName)
	}
	return resolved
}

// ScanTriggers is the scan rule (spec/metadata-model.md "Triggers"; SchemaGuard and every CLI command that
// enumerates entities): a trigger class linked by two entities, or two classes declaring one name, is one
// MAP-029 per conflict — a lone load cannot see either. Go has no assembly to scan, so the scan runs over
// the explicit lists a Registry holds: its entities (trigger classes and owned types among them are skipped,
// as the C# scan skips them) and its trigger classes (so a class no entity links still counts for the
// name rule). An entity whose own load fails contributes nothing here; its errors are reported where it loads.
func (l *Loader) ScanTriggers(entities []reflect.Type, triggerTypes []reflect.Type) []*core.Error {
	linkedBy := map[reflect.Type][]string{}
	var classes []reflect.Type
	known := map[reflect.Type]bool{}
	note := func(t reflect.Type) {
		if !known[t] {
			known[t] = true
			classes = append(classes, t)
		}
	}

	// The links are read from the descriptors, not from loaded maps: an entity whose load fails for another
	// reason still links what it links (the reference reads the attributes the same way).
	for _, entityType := range entities {
		entityType = EntityType(entityType)
		if entityType == nil || core.IsTriggerType(entityType) || core.IsOwnedType(entityType) {
			if entityType != nil && core.IsTriggerType(entityType) {
				note(entityType)
			}
			continue
		}
		def, ok := Descriptor(entityType)
		if !ok {
			continue
		}
		seen := map[reflect.Type]bool{}
		for _, link := range def.Triggers {
			linkedType := EntityType(link.Type)
			if linkedType == nil || seen[linkedType] || !core.IsTriggerType(linkedType) {
				continue
			}
			seen[linkedType] = true
			linkedBy[linkedType] = append(linkedBy[linkedType], entityType.Name())
			note(linkedType)
		}
	}
	for _, triggerType := range triggerTypes {
		if core.IsTriggerType(triggerType) {
			note(EntityType(triggerType))
		}
	}

	var conflicts []*core.Error
	sort.Slice(classes, func(i, j int) bool { return core.TypeName(classes[i]) < core.TypeName(classes[j]) })
	for _, class := range classes {
		if owners := linkedBy[class]; len(owners) > 1 {
			sort.Strings(owners)
			conflicts = append(conflicts, core.Errorf("MAP-029", class.Name(),
				"the trigger class %s is linked by %s; a trigger belongs to one table", core.TypeName(class), strings.Join(owners, " and ")))
		}
	}

	// Names compare case-insensitively (the scan rule, ADR-0042); the error names the first spelling.
	byName := map[string][]reflect.Type{}
	spelling := map[string]string{}
	var keys []string
	for _, class := range classes {
		declaration, _ := core.TriggerDeclarationOf(class)
		name := strings.TrimSpace(declaration.Name)
		if name == "" {
			continue
		}
		key := strings.ToLower(name)
		if _, seen := byName[key]; !seen {
			keys = append(keys, key)
			spelling[key] = name
		}
		byName[key] = append(byName[key], class)
	}
	sort.Strings(keys)
	for _, key := range keys {
		name := spelling[key]
		if declarers := byName[key]; len(declarers) > 1 {
			spelled := make([]string, len(declarers))
			for i, d := range declarers {
				spelled[i] = core.TypeName(d)
			}
			conflicts = append(conflicts, core.Errorf("MAP-029", name,
				"the trigger name '%s' is declared by %s; names are unique across the model", name, strings.Join(spelled, " and ")))
		}
	}
	return conflicts
}
