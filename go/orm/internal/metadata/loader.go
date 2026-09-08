package metadata

import (
	"reflect"
	"sync"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
)

var entityDefinerType = reflect.TypeFor[core.EntityDefiner]()

// Loader is the single entry point for metadata (§7.2). Precedence per type:
// an explicit registration, else the declaration loader when the type carries
// any mapping declaration (an `orm` tag or an Entity() descriptor), else the
// convention loader. Maps are cached per loader instance — the session owns
// one, so an entity's map loads once per session; a type that fails to load
// returns MappingErrors with every violation.
type Loader struct {
	options *Options
	mu      sync.Mutex
	cache   map[reflect.Type]*core.EntityMap
}

// NewLoader creates a loader; nil options mean the defaults (snake_case, no explicit maps).
func NewLoader(options *Options) *Loader {
	if options == nil {
		options = &Options{}
	}
	return &Loader{options: options, cache: map[reflect.Type]*core.EntityMap{}}
}

// Options is the loader's configuration.
func (l *Loader) Options() *Options { return l.options }

// Load returns the map for a type (a pointer resolves to its struct).
func (l *Loader) Load(entityType reflect.Type) (*core.EntityMap, error) {
	t := EntityType(entityType)
	if t == nil || t.Kind() != reflect.Struct {
		return nil, core.Errorf("MAP-023", core.TypeName(entityType), "only struct types map to entities")
	}
	l.mu.Lock()
	cached, ok := l.cache[t]
	l.mu.Unlock()
	if ok {
		return cached, nil
	}
	// Loading runs unlocked: validating a relationship loads the target's map,
	// which may load this type again — a lock held across the load would deadlock.
	m, err := l.load(t)
	if err != nil {
		return nil, err
	}
	l.mu.Lock()
	if existing, ok := l.cache[t]; ok {
		m = existing
	} else {
		l.cache[t] = m
	}
	l.mu.Unlock()
	return m, nil
}

// Load is the generic form: metadata.Load[User](loader).
func Load[T any](l *Loader) (*core.EntityMap, error) {
	return l.Load(reflect.TypeFor[T]())
}

func (l *Loader) load(t reflect.Type) (*core.EntityMap, error) {
	if core.IsOwnedType(t) {
		// An owned value type (ADR-0030) has no map of its own; it is read
		// through its owner. Loading it directly is a caller error.
		return nil, &core.MappingErrors{EntityType: t, Errors: []*core.Error{core.NewError("MAP-024", t.Name(),
			"is an owned value type (orm.OwnedType), not an entity; it maps only as a member of its owner")}}
	}
	if explicit, ok := l.options.explicitFor(t); ok {
		return explicit.Build(l.options.Convention())
	}
	if HasMappingDeclarations(t) {
		return loadFromDeclarations(l, t)
	}
	return loadByConvention(l, t)
}

// HasMappingDeclarations reports whether the type carries any mapping
// declaration — an Entity() descriptor, or an `orm` tag on any exported field,
// embedded structs included (the C# HasMappingAttributes; used by tooling too).
func HasMappingDeclarations(t reflect.Type) bool {
	t = EntityType(t)
	if t == nil || t.Kind() != reflect.Struct {
		return false
	}
	if reflect.PointerTo(t).Implements(entityDefinerType) {
		return true
	}
	return hasTaggedField(t, map[reflect.Type]bool{})
}

func hasTaggedField(t reflect.Type, visited map[reflect.Type]bool) bool {
	if visited[t] {
		return false
	}
	visited[t] = true
	for i := range t.NumField() {
		field := t.Field(i)
		if _, tagged := field.Tag.Lookup(TagName); tagged {
			return true
		}
		if field.Anonymous {
			embedded := EntityType(field.Type)
			if embedded.Kind() == reflect.Struct && hasTaggedField(embedded, visited) {
				return true
			}
		}
	}
	return false
}

// Descriptor returns the type's EntityDef when it implements EntityDefiner
// (value or pointer receiver), reading it from a zero value.
func Descriptor(t reflect.Type) (core.EntityDef, bool) {
	t = EntityType(t)
	if t == nil || t.Kind() != reflect.Struct || !reflect.PointerTo(t).Implements(entityDefinerType) {
		return core.EntityDef{}, false
	}
	return reflect.New(t).Interface().(core.EntityDefiner).Entity(), true
}

// TagName is the struct tag key every mapping declaration lives under.
const TagName = "orm"
