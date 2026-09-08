// Package core holds the spec-level contracts of the Go port: the error values,
// the metadata model (EntityMap and friends), the value types, the naming
// convention, the type-handler seam, the dialect seam, the query AST, and the
// canonical JSON writer. Every other package reads these and nothing else
// (CLAUDE.md §7.1, §10); user code sees them through the orm package's aliases.
// The package is internal so the port has one public surface — orm, orm/sqlite,
// orm/cli — exactly like the C# reference's SimpleOrm, SimpleOrm.Sqlite,
// SimpleOrm.Cli assemblies (CODING-STANDARD §1).
package core

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
)

// Error is a runtime failure with a stable code from spec/errors.md (§2 "errors
// name things"): the code, the thing at fault (a query name, Type.Property, a
// parameter, the session), and what was expected. Errors are values — callers
// use errors.As, never panics (CODING-STANDARD §4).
type Error struct {
	// Code is the stable code, e.g. PRM-001 or QRY-002 — the cross-language contract.
	Code string
	// Target is what the error names: a query, a property, a parameter, a session.
	Target string
	// Message says what was expected and, when there is something to do, what to do.
	Message string
}

// NewError builds an Error; the message reads "<code> <target>: <message>".
func NewError(code, target, message string) *Error {
	return &Error{Code: code, Target: target, Message: message}
}

// Errorf is NewError with a formatted message.
func Errorf(code, target, format string, args ...any) *Error {
	return &Error{Code: code, Target: target, Message: fmt.Sprintf(format, args...)}
}

func (e *Error) Error() string { return e.Code + " " + e.Target + ": " + e.Message }

// MappingErrors is every violation found while loading one type's EntityMap
// (spec/metadata-model.md: loaders never stop at the first error).
type MappingErrors struct {
	EntityType reflect.Type
	Errors     []*Error
}

func (e *MappingErrors) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Mapping of '%s' failed with %d %s", TypeName(e.EntityType), len(e.Errors), plural(len(e.Errors), "error"))
	for _, err := range e.Errors {
		b.WriteString("\n  ")
		b.WriteString(err.Error())
	}
	return b.String()
}

// ValidationError is one SchemaGuard violation: a stable code, the source
// (registry entry or entity), and what was expected (§7.20).
type ValidationError struct {
	Code    string
	Source  string
	Message string
}

func (e *ValidationError) String() string { return e.Code + " " + e.Source + ": " + e.Message }

// ValidationErrors is SchemaGuard's complete report — every violation across
// every registry entry and entity, never just the first (§7.20); fail fast in
// all environments, no warn-only mode (§7.21).
type ValidationErrors struct {
	Errors []*ValidationError
}

func (e *ValidationErrors) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Schema validation failed with %d %s", len(e.Errors), plural(len(e.Errors), "violation"))
	var order []string
	bySource := map[string][]*ValidationError{}
	for _, err := range e.Errors {
		if _, seen := bySource[err.Source]; !seen {
			order = append(order, err.Source)
		}
		bySource[err.Source] = append(bySource[err.Source], err)
	}
	for _, source := range order {
		b.WriteString("\n  ")
		b.WriteString(source)
		for _, err := range bySource[source] {
			b.WriteString("\n    ")
			b.WriteString(err.Code)
			b.WriteString(": ")
			b.WriteString(err.Message)
		}
	}
	return b.String()
}

// ConcurrencyErrorCode is the one code a ConcurrencyError carries.
const ConcurrencyErrorCode = "CRUD-010"

// ConcurrencyError is an optimistic-concurrency conflict (§7.16): an update or
// delete carrying a version affected zero rows. Its own type, so callers can
// catch the reload-and-retry case specifically.
type ConcurrencyError struct {
	Target  string
	Message string
}

func (e *ConcurrencyError) Error() string {
	return ConcurrencyErrorCode + " " + e.Target + ": " + e.Message
}

// Code is CRUD-010, the way Error exposes its code.
func (e *ConcurrencyError) Code() string { return ConcurrencyErrorCode }

// CodeOf returns the stable code err carries: an Error's code, CRUD-010 for a
// ConcurrencyError, the first entry's code for an aggregate, "" for anything
// else. Tests and the CLI assert codes, never message text (CODING-STANDARD §7).
func CodeOf(err error) string {
	var single *Error
	if errors.As(err, &single) {
		return single.Code
	}
	var concurrency *ConcurrencyError
	if errors.As(err, &concurrency) {
		return ConcurrencyErrorCode
	}
	var mapping *MappingErrors
	if errors.As(err, &mapping) && len(mapping.Errors) > 0 {
		return mapping.Errors[0].Code
	}
	var validation *ValidationErrors
	if errors.As(err, &validation) && len(validation.Errors) > 0 {
		return validation.Errors[0].Code
	}
	return ""
}

// HasCode reports whether err carries the code — directly, or inside an
// aggregate (MappingErrors, ValidationErrors).
func HasCode(err error, code string) bool {
	var single *Error
	if errors.As(err, &single) {
		return single.Code == code
	}
	var concurrency *ConcurrencyError
	if errors.As(err, &concurrency) {
		return code == ConcurrencyErrorCode
	}
	var mapping *MappingErrors
	if errors.As(err, &mapping) {
		for _, e := range mapping.Errors {
			if e.Code == code {
				return true
			}
		}
		return false
	}
	var validation *ValidationErrors
	if errors.As(err, &validation) {
		for _, e := range validation.Errors {
			if e.Code == code {
				return true
			}
		}
	}
	return false
}

// TypeName is the qualified name used in messages: "pkg.Type" for named types,
// reflect's string form otherwise.
func TypeName(t reflect.Type) string {
	if t == nil {
		return "<nil>"
	}
	if t.PkgPath() != "" && t.Name() != "" {
		return t.PkgPath() + "." + t.Name()
	}
	return t.String()
}

func plural(count int, noun string) string {
	if count == 1 {
		return noun
	}
	return noun + "s"
}
