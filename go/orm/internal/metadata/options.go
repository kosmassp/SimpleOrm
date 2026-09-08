// Package metadata produces EntityMaps (§7.2): the declaration loader (struct
// tags plus the EntityDef descriptor), the convention loader, the manual
// builder, and the JSON export. Nothing outside this package reads tags or
// descriptors — every other subsystem reads the EntityMap the Loader hands out
// (§7.1, §10.1).
package metadata

import (
	"reflect"

	"github.com/kosmassp/SimpleOrm/go/orm/internal/core"
)

// ExplicitMap is a manual map registration (the C# EntityMapBuilder<T> passed
// to MappingOptions.Register): it names its entity type and builds the map
// under the options' naming convention.
type ExplicitMap interface {
	EntityType() reflect.Type
	Build(convention core.NamingConvention) (*core.EntityMap, error)
}

// Options configures metadata loading: the naming convention (default
// snake_case) and explicit registrations, which take precedence over
// declarations and conventions (§7.2: explicit → declared → convention). The
// zero value is usable.
type Options struct {
	// NamingConvention derives names wherever none is explicit; nil means snake_case.
	NamingConvention core.NamingConvention
	explicit         map[reflect.Type]ExplicitMap
}

// Register adds a manual map for its entity type, overriding tags and conventions; returns the options for chaining.
func (o *Options) Register(m ExplicitMap) *Options {
	if o.explicit == nil {
		o.explicit = map[reflect.Type]ExplicitMap{}
	}
	o.explicit[EntityType(m.EntityType())] = m
	return o
}

// Convention is the effective naming convention.
func (o *Options) Convention() core.NamingConvention {
	if o == nil || o.NamingConvention == nil {
		return core.SnakeCaseConvention
	}
	return o.NamingConvention
}

func (o *Options) explicitFor(t reflect.Type) (ExplicitMap, bool) {
	if o == nil || o.explicit == nil {
		return nil, false
	}
	m, ok := o.explicit[t]
	return m, ok
}

// EntityType normalizes a type the way every loader entry point sees it: a
// pointer to a struct resolves to the struct.
func EntityType(t reflect.Type) reflect.Type {
	for t != nil && t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	return t
}
