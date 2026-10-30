package orm

// Test-only access to the performance pass's internal rules (compiled into the orm package's tests only).

// KeyOrderIsValueOrder is the sort-skip rule (loading_keys.go) for m's key under dialect.
func KeyOrderIsValueOrder(dialect Dialect, m *EntityMap) bool {
	return keyOrderIsValueOrder(dialect, m, m.KeyProperties)
}
