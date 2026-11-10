package scim

import (
	"encoding/json"
	"strings"
	"testing"
)

// TestParseUserPatch 覆盖 active / userName / name 与非法 path。
func TestParseUserPatch(t *testing.T) {
	request := patchRequest{Operations: []patchOperation{
		{Op: "replace", Path: "active", Value: json.RawMessage(`false`)},
		{Op: "replace", Path: "userName", Value: json.RawMessage(`"bob@corp.example"`)},
		{Op: "replace", Value: json.RawMessage(`{"displayName":"Bob"}`)},
	}}
	patch, err := parseUserPatch(request)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if patch.Active == nil || *patch.Active {
		t.Fatalf("active = %v, want false", patch.Active)
	}
	if patch.UserName == nil || *patch.UserName != "bob@corp.example" {
		t.Fatalf("userName = %v", patch.UserName)
	}
	if patch.DisplayName == nil || *patch.DisplayName != "Bob" {
		t.Fatalf("displayName = %v", patch.DisplayName)
	}

	namePatch, err := parseUserPatch(patchRequest{Operations: []patchOperation{
		{Op: "replace", Path: "name", Value: json.RawMessage(`{"givenName":"Alice","familyName":"Chen"}`)},
	}})
	if err != nil {
		t.Fatalf("parse name: %v", err)
	}
	if namePatch.DisplayName == nil || *namePatch.DisplayName != "Alice Chen" {
		t.Fatalf("displayName = %v, want joined name", namePatch.DisplayName)
	}

	removePatch, err := parseUserPatch(patchRequest{Operations: []patchOperation{
		{Op: "remove", Path: "displayName"},
	}})
	if err != nil {
		t.Fatalf("parse remove: %v", err)
	}
	if !removePatch.ClearDisplayName {
		t.Fatalf("ClearDisplayName = false, want true")
	}
}

// TestParseUserPatchRejects 覆盖协议禁止项。
func TestParseUserPatchRejects(t *testing.T) {
	cases := []struct {
		name     string
		op       patchOperation
		contains string
	}{
		{
			name:     "password rejected",
			op:       patchOperation{Op: "replace", Path: "password", Value: json.RawMessage(`"x"`)},
			contains: "mutability",
		},
		{
			name:     "groups read only",
			op:       patchOperation{Op: "replace", Path: "groups", Value: json.RawMessage(`[]`)},
			contains: "mutability",
		},
		{
			name:     "unknown path",
			op:       patchOperation{Op: "replace", Path: "nickName", Value: json.RawMessage(`"x"`)},
			contains: "invalidPath",
		},
		{
			name:     "remove username",
			op:       patchOperation{Op: "remove", Path: "userName"},
			contains: "noTarget",
		},
		{
			name:     "unsupported op",
			op:       patchOperation{Op: "move", Path: "active", Value: json.RawMessage(`true`)},
			contains: "invalidSyntax",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := parseUserPatch(patchRequest{Operations: []patchOperation{tc.op}})
			if err == nil {
				t.Fatalf("want error")
			}
			if !strings.Contains(err.Error(), tc.contains) {
				t.Fatalf("err = %v, want %s", err, tc.contains)
			}
		})
	}
}

// TestParseGroupPatchMembers 覆盖成员增删与成员定位 path。
func TestParseGroupPatchMembers(t *testing.T) {
	patch, err := parseGroupPatch(patchRequest{Operations: []patchOperation{
		{Op: "add", Path: "members", Value: json.RawMessage(`[{"value":"1001"}]`)},
	}})
	if err != nil {
		t.Fatalf("parse add: %v", err)
	}
	if len(patch.AddMembers) != 1 || patch.AddMembers[0] != "1001" {
		t.Fatalf("AddMembers = %v", patch.AddMembers)
	}

	patch, err = parseGroupPatch(patchRequest{Operations: []patchOperation{
		{Op: "remove", Path: `members[value eq "1001"]`},
	}})
	if err != nil {
		t.Fatalf("parse remove by path: %v", err)
	}
	if len(patch.RemoveMembers) != 1 || patch.RemoveMembers[0] != "1001" {
		t.Fatalf("RemoveMembers = %v", patch.RemoveMembers)
	}

	// remove 整个 members 等价于清空。
	patch, err = parseGroupPatch(patchRequest{Operations: []patchOperation{
		{Op: "remove", Path: "members"},
	}})
	if err != nil {
		t.Fatalf("parse clear: %v", err)
	}
	if patch.ReplaceMembers == nil || len(patch.ReplaceMembers) != 0 {
		t.Fatalf("ReplaceMembers = %v, want non-nil empty", patch.ReplaceMembers)
	}
}
