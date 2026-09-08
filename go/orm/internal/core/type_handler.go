package core

import "reflect"

// TypeHandler converts between a Go type and its database representation
// (§7.9): the extension point for anything outside the fixed conversion table.
// No reflection-based guessing — unregistered types fail with MAP-030.
type TypeHandler[T any] interface {
	// Parse: database value → Go value. Never receives NULL.
	Parse(databaseValue any) (T, error)
	// Format: Go value → database value (a type the driver stores natively).
	Format(value T) (any, error)
}

type handlerFuncs struct {
	parse  func(any) (any, error)
	format func(any) (any, error)
}

// TypeHandlerRegistry is the per-options registry of handlers. A nil registry
// holds nothing, so zero options work without one.
type TypeHandlerRegistry struct {
	handlers map[reflect.Type]handlerFuncs
}

// NewTypeHandlerRegistry is an empty registry.
func NewTypeHandlerRegistry() *TypeHandlerRegistry {
	return &TypeHandlerRegistry{handlers: map[reflect.Type]handlerFuncs{}}
}

// RegisterHandler registers a handler for T (methods cannot take type
// parameters, so this is a function); returns the registry for chaining.
func RegisterHandler[T any](registry *TypeHandlerRegistry, handler TypeHandler[T]) *TypeHandlerRegistry {
	if registry.handlers == nil {
		registry.handlers = map[reflect.Type]handlerFuncs{}
	}
	registry.handlers[reflect.TypeFor[T]()] = handlerFuncs{
		parse: func(value any) (any, error) { return handler.Parse(value) },
		format: func(value any) (any, error) {
			typed, ok := value.(T)
			if !ok {
				return nil, Errorf("MAP-030", TypeName(reflect.TypeFor[T]()), "handler received a %T", value)
			}
			return handler.Format(typed)
		},
	}
	return registry
}

// Contains reports whether a handler is registered for t (a nullable pointer resolves to its element).
func (r *TypeHandlerRegistry) Contains(t reflect.Type) bool {
	if r == nil {
		return false
	}
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	_, ok := r.handlers[t]
	return ok
}

// Parse runs the handler for t; handled is false when none is registered.
func (r *TypeHandlerRegistry) Parse(t reflect.Type, databaseValue any) (value any, handled bool, err error) {
	if r == nil {
		return nil, false, nil
	}
	if t.Kind() == reflect.Pointer {
		t = t.Elem()
	}
	h, ok := r.handlers[t]
	if !ok {
		return nil, false, nil
	}
	value, err = h.parse(databaseValue)
	return value, true, err
}

// Format runs the handler for the value's dynamic type; handled is false when none is registered.
func (r *TypeHandlerRegistry) Format(value any) (databaseValue any, handled bool, err error) {
	if r == nil || value == nil {
		return nil, false, nil
	}
	h, ok := r.handlers[reflect.TypeOf(value)]
	if !ok {
		return nil, false, nil
	}
	databaseValue, err = h.format(value)
	return databaseValue, true, err
}
