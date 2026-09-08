package core

import "testing"

// The normative vectors of spec/metadata-model.md: every implementation must match.
func TestToSnakeCase_NormativeVectors(t *testing.T) {
	vectors := map[string]string{
		"Name":          "name",
		"UserId":        "user_id",
		"UserID":        "user_id",
		"APIKey":        "api_key",
		"HTMLParser":    "html_parser",
		"Address2":      "address2",
		"Address2B":     "address2_b",
		"CreatedAtUtc":  "created_at_utc",
		"ID":            "id",
		"camelCase":     "camel_case",
		"already_snake": "already_snake",
		"":              "",
	}
	for input, expected := range vectors {
		if got := ToSnakeCase(input); got != expected {
			t.Errorf("ToSnakeCase(%q) = %q, want %q", input, got, expected)
		}
	}
}

func TestSnakeCase_DerivesTableAndIndexNames(t *testing.T) {
	convention := SnakeCase{}
	if got := convention.TableName("TransactionDetail"); got != "transaction_detail" {
		t.Errorf("table name: got %q", got)
	}
	if got := convention.IndexName("transactions", []string{"status", "created_at"}); got != "ix_transactions_status_created_at" {
		t.Errorf("index name: got %q", got)
	}
}

func TestToPascalCase_SpellsGoInitialismsTheReferenceWay(t *testing.T) {
	vectors := map[string]string{"UserID": "UserId", "TransactionID": "TransactionId", "RoleId": "RoleId", "Name": "Name"}
	for input, expected := range vectors {
		if got := ToPascalCase(input); got != expected {
			t.Errorf("ToPascalCase(%q) = %q, want %q", input, got, expected)
		}
	}
}
