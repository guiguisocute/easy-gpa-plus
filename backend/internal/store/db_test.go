package store

import "testing"

// The cross-tenant enforcement itself is covered by the integration test that
// starts PostgreSQL. This small test keeps invalid context from reaching a pool.
func TestBeginTenantRejectsInvalidID(t *testing.T) {
	if _, err := BeginTenant(t.Context(), nil, 0); err == nil {
		t.Fatal("expected invalid class ID error")
	}
}
