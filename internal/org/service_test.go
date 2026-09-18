package org

import (
	"context"
	"errors"
	"testing"

	"github.com/Autonex009/autonex-crm-api/pkg/apperr"
)

// The guard checks below all run before the store is touched, so a zero Service
// is enough: reaching the database would be the bug they exist to catch.
func TestUpdateMemberRoleRejectsInvalidRole(t *testing.T) {
	svc := &Service{}
	for _, role := range []string{"superman", "guest", "", " ", "ADMIN", "Manager"} {
		_, err := svc.UpdateMemberRole(context.Background(), "org-1", "actor-1", "owner", "user-1", role)
		if err == nil {
			t.Errorf("expected validation error for role %q, got nil", role)
			continue
		}
		if !apperr.IsValidation(err) {
			t.Errorf("expected apperr.Validation for role %q, got %v", role, err)
		}
	}
}

func TestUpdateMemberRoleRejectsSelfEdit(t *testing.T) {
	svc := &Service{}
	_, err := svc.UpdateMemberRole(context.Background(), "org-1", "user-1", "owner", "user-1", "sales")
	if !errors.Is(err, ErrSelfRoleChange) {
		t.Fatalf("err = %v, want ErrSelfRoleChange", err)
	}
}

// Only an owner may mint another owner — otherwise an admin could hand
// themselves the rights the owner-only guard is meant to withhold.
func TestUpdateMemberRoleOwnerGrantIsOwnerOnly(t *testing.T) {
	svc := &Service{}
	for _, actor := range []string{"admin", "account_manager", "sales", ""} {
		_, err := svc.UpdateMemberRole(context.Background(), "org-1", "actor-1", actor, "user-1", "owner")
		if !errors.Is(err, ErrOwnerOnly) {
			t.Errorf("actor %q: err = %v, want ErrOwnerOnly", actor, err)
		}
	}
}
