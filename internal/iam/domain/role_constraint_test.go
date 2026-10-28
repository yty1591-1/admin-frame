package domain

import (
	"errors"
	"fmt"
	"testing"
)

func TestNewRoleConstraint(t *testing.T) {
	constraint, err := NewRoleConstraint("role-1", []string{"role-3", "role-2"})
	if err != nil {
		t.Fatalf("NewRoleConstraint: %v", err)
	}
	if constraint.Empty() {
		t.Fatal("constraint must not be empty")
	}
	sorted := constraint.Sorted()
	if sorted[0] != "role-2" || sorted[1] != "role-3" {
		t.Fatalf("sorted=%v", sorted)
	}
	sorted[0] = "mutated"
	if constraint.RequiredRoleIDs[0] == "mutated" {
		t.Fatal("Sorted must return a copy")
	}

	empty, err := NewRoleConstraint("role-1", nil)
	if err != nil || !empty.Empty() {
		t.Fatalf("empty constraint err=%v empty=%v", err, empty.Empty())
	}
}

func TestNewRoleConstraintValidation(t *testing.T) {
	tooMany := make([]string, 0, MaxPrerequisitesPerRole+1)
	for index := 0; index <= MaxPrerequisitesPerRole; index++ {
		tooMany = append(tooMany, fmt.Sprintf("role-%03d", index))
	}

	cases := []struct {
		name     string
		roleID   string
		required []string
		want     error
	}{
		{name: "missing role id", roleID: "", required: []string{"role-2"}, want: ErrEmptyInput},
		{name: "self prerequisite", roleID: "role-1", required: []string{"role-1"}, want: ErrInvalidRoleConstraint},
		{name: "duplicate prerequisite", roleID: "role-1", required: []string{"role-2", "role-2"}, want: ErrInvalidRoleConstraint},
		{name: "blank prerequisite", roleID: "role-1", required: []string{" "}, want: ErrInvalidRoleConstraint},
		{name: "too many prerequisites", roleID: "role-1", required: tooMany, want: ErrInvalidRoleConstraint},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewRoleConstraint(tc.roleID, tc.required)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
		})
	}
}
