package core

import (
	"strings"
	"unicode"
)

// NamingConvention translates Go names to database names wherever a name is
// derived rather than given explicitly: a bare `column` tag, a type without a
// declared table name, and derived index names (spec/metadata-model.md). An
// explicit name always bypasses it. Configurable per options; the shipped
// default is SnakeCase.
type NamingConvention interface {
	// ColumnName for a field name (UserID → user_id).
	ColumnName(propertyName string) string
	// TableName for a type name (TransactionDetail → transaction_detail; never pluralized).
	TableName(typeName string) string
	// IndexName for a table and its column names, in index order (ix_<table>_<col>[_<col>…]).
	IndexName(tableName string, columnNames []string) string
}

// SnakeCase is the default convention: PascalCase/camelCase → snake_case. The
// algorithm is part of the spec (every port must produce identical names from
// its own idioms), pinned by the normative vectors: UserId → user_id, UserID →
// user_id, APIKey → api_key, HTMLParser → html_parser, Address2 → address2,
// Address2B → address2_b. Stateless — the zero value is the convention.
type SnakeCase struct{}

// SnakeCaseConvention is the shared default instance.
var SnakeCaseConvention NamingConvention = SnakeCase{}

func (SnakeCase) ColumnName(propertyName string) string { return ToSnakeCase(propertyName) }

func (SnakeCase) TableName(typeName string) string { return ToSnakeCase(typeName) }

func (SnakeCase) IndexName(tableName string, columnNames []string) string {
	var b strings.Builder
	b.WriteString("ix_")
	b.WriteString(tableName)
	for _, column := range columnNames {
		b.WriteByte('_')
		b.WriteString(column)
	}
	return b.String()
}

// ToSnakeCase is the one snake_case implementation (CODING-STANDARD §8): an
// underscore is inserted before an upper-case letter that follows a lower-case
// letter or digit, or that starts the last word of an acronym run (an upper
// followed by a lower); everything is lower-cased.
func ToSnakeCase(name string) string {
	runes := []rune(name)
	if len(runes) == 0 {
		return name
	}
	var b strings.Builder
	b.Grow(len(name) + 4)
	for i, c := range runes {
		if unicode.IsUpper(c) && i > 0 {
			previous := runes[i-1]
			startsWordAfterLowerOrDigit := unicode.IsLower(previous) || unicode.IsDigit(previous)
			endsAcronymRun := unicode.IsUpper(previous) && i+1 < len(runes) && unicode.IsLower(runes[i+1])
			if startsWordAfterLowerOrDigit || endsAcronymRun {
				b.WriteByte('_')
			}
		}
		b.WriteRune(unicode.ToLower(c))
	}
	return b.String()
}

// ToPascalCase is the export's interim spelling of a Go field name
// (CODING-STANDARD §10, ADR-0027): each snake_case word capitalized, so UserID
// exports as UserId — the C# spelling the pinned entity files carry until the
// spec switches targetForeignKeyProperties to column names.
func ToPascalCase(name string) string {
	var b strings.Builder
	for _, word := range strings.Split(ToSnakeCase(name), "_") {
		if word == "" {
			continue
		}
		runes := []rune(word)
		b.WriteRune(unicode.ToUpper(runes[0]))
		b.WriteString(string(runes[1:]))
	}
	return b.String()
}
