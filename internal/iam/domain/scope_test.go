package domain

import (
	"errors"
	"strconv"
	"testing"
)

func TestChannelValidate(t *testing.T) {
	for _, c := range []Channel{ChannelAdmin, ChannelBiz} {
		if err := c.Validate(); err != nil {
			t.Fatalf("%s: %v", c, err)
		}
		if c.String() != string(c) {
			t.Fatalf("String()=%q", c.String())
		}
	}
	for _, c := range []Channel{"", "c", "both", "ADMIN"} {
		err := c.Validate()
		if !errors.Is(err, ErrInvalidChannel) {
			t.Fatalf("channel %q: err=%v", c, err)
		}
	}
}

func TestEnumValidate(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want error
	}{
		{"role scope platform", RoleScopePlatform.Validate(), nil},
		{"role scope merchant", RoleScopeMerchant.Validate(), nil},
		{"role scope bad", RoleScopeType("global").Validate(), ErrInvalidRoleScope},
		{"role type system", RoleTypeSystem.Validate(), nil},
		{"role type custom", RoleTypeCustom.Validate(), nil},
		{"role type breakglass", RoleTypeBreakglass.Validate(), nil},
		{"role type bad", RoleType("root").Validate(), ErrInvalidRoleScope},
		{"status active", RoleStatusActive.Validate(), nil},
		{"status disabled", RoleStatusDisabled.Validate(), nil},
		{"status bad", RoleStatus("frozen").Validate(), ErrRoleNotActive},
		{"subject staff", SubjectStaff.Validate(), nil},
		{"subject merchant staff", SubjectMerchantStaff.Validate(), nil},
		{"subject bad", SubjectType("guest").Validate(), ErrInvalidSubjectType},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if tc.want == nil {
				if tc.err != nil {
					t.Fatalf("unexpected error: %v", tc.err)
				}
				return
			}
			if !errors.Is(tc.err, tc.want) {
				t.Fatalf("err=%v, want %v", tc.err, tc.want)
			}
		})
	}
}

func TestDataScopeTypeValidate(t *testing.T) {
	cases := []struct {
		name    string
		scope   DataScopeType
		channel Channel
		wantErr bool
	}{
		{name: "all admin", scope: DataScopeAll, channel: ChannelAdmin},
		{name: "all biz rejected", scope: DataScopeAll, channel: ChannelBiz, wantErr: true},
		{name: "merchant admin", scope: DataScopeMerchant, channel: ChannelAdmin},
		{name: "merchant biz", scope: DataScopeMerchant, channel: ChannelBiz},
		{name: "self biz", scope: DataScopeSelf, channel: ChannelBiz},
		{name: "merchant_set admin", scope: DataScopeMerchantSet, channel: ChannelAdmin},
		{name: "merchant_set biz rejected", scope: DataScopeMerchantSet, channel: ChannelBiz, wantErr: true},
		{name: "unknown", scope: DataScopeType("region"), channel: ChannelAdmin, wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.scope.Validate(tc.channel)
			if tc.wantErr {
				if !errors.Is(err, ErrInvalidDataScope) {
					t.Fatalf("err=%v, want ErrInvalidDataScope", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestNewDataScopeValidation(t *testing.T) {
	cases := []struct {
		name    string
		scope   func() (DataScope, error)
		wantErr error
	}{
		{
			name: "valid merchant",
			scope: func() (DataScope, error) {
				return NewDataScope("order", DataScopeMerchant, nil)
			},
		},
		{
			name: "valid wildcard resource",
			scope: func() (DataScope, error) {
				return NewDataScope(ResourceAll, DataScopeSelf, nil)
			},
		},
		{
			name: "valid merchant set",
			scope: func() (DataScope, error) {
				return NewDataScope("order", DataScopeMerchantSet, []string{"m2", "m1", "m1"})
			},
		},
		{
			name: "empty resource",
			scope: func() (DataScope, error) {
				return NewDataScope("", DataScopeMerchant, nil)
			},
			wantErr: ErrInvalidDataScope,
		},
		{
			name: "bad resource",
			scope: func() (DataScope, error) {
				return NewDataScope("Order-Item", DataScopeMerchant, nil)
			},
			wantErr: ErrInvalidDataScope,
		},
		{
			name: "bad type",
			scope: func() (DataScope, error) {
				return NewDataScope("order", DataScopeType("region"), nil)
			},
			wantErr: ErrInvalidDataScope,
		},
		{
			name: "merchant set without ids",
			scope: func() (DataScope, error) {
				return NewDataScope("order", DataScopeMerchantSet, nil)
			},
			wantErr: ErrInvalidDataScope,
		},
		{
			name: "merchant set too large",
			scope: func() (DataScope, error) {
				ids := make([]string, 0, MaxMerchantSetSize+1)
				for i := 0; i <= MaxMerchantSetSize; i++ {
					ids = append(ids, "m"+strconv.Itoa(i))
				}
				return NewDataScope("order", DataScopeMerchantSet, ids)
			},
			wantErr: ErrMerchantSetTooLarge,
		},
		{
			name: "non set type with ids",
			scope: func() (DataScope, error) {
				return NewDataScope("order", DataScopeMerchant, []string{"m1"})
			},
			wantErr: ErrInvalidDataScope,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			scope, err := tc.scope()
			if tc.wantErr != nil {
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("err=%v, want %v", err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if scope.Resource == "" {
				t.Fatal("resource must be kept")
			}
		})
	}
}

func TestDataScopeValidateRejectsEmptyMerchantID(t *testing.T) {
	// 绕过 NewDataScope 的清洗，验证结构校验本身不会放过空商户 ID。
	scope := DataScope{Resource: "order", Type: DataScopeMerchantSet, MerchantIDs: []string{""}}
	if err := scope.Validate(ChannelAdmin); !errors.Is(err, ErrInvalidDataScope) {
		t.Fatalf("err=%v, want ErrInvalidDataScope", err)
	}
}

func TestNewDataScopeNormalizesMerchantIDs(t *testing.T) {
	scope, err := NewDataScope("order", DataScopeMerchantSet, []string{"m2", "", "m1", "m2"})
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"m1", "m2"}
	if len(scope.MerchantIDs) != len(want) {
		t.Fatalf("ids=%v", scope.MerchantIDs)
	}
	for i := range want {
		if scope.MerchantIDs[i] != want[i] {
			t.Fatalf("ids[%d]=%s, want %s", i, scope.MerchantIDs[i], want[i])
		}
	}
}

func TestDataScopeEqual(t *testing.T) {
	left := DataScope{Resource: "order", Type: DataScopeMerchantSet, MerchantIDs: []string{"m1", "m2"}}
	right := DataScope{Resource: "order", Type: DataScopeMerchantSet, MerchantIDs: []string{"m2", "m1"}}
	if !left.Equal(right) {
		t.Fatal("order-insensitive equality expected")
	}
	if left.Equal(DataScope{Resource: "order", Type: DataScopeMerchant}) {
		t.Fatal("different type must not be equal")
	}
	if left.Equal(DataScope{Resource: "file", Type: DataScopeMerchantSet, MerchantIDs: left.MerchantIDs}) {
		t.Fatal("different resource must not be equal")
	}
	if left.Equal(DataScope{Resource: "order", Type: DataScopeMerchantSet, MerchantIDs: []string{"m1"}}) {
		t.Fatal("different id count must not be equal")
	}
}

// TestDataScopeMerchant 与 TestDataScopeSelf 是 TESTING_STANDARDS 必测函数。
func TestDataScopeMerchant(t *testing.T) {
	scope, err := NewDataScope("order", DataScopeMerchant, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := scope.Validate(ChannelBiz); err != nil {
		t.Fatalf("merchant scope must be allowed for biz: %v", err)
	}
	if scope.Type != DataScopeMerchant {
		t.Fatalf("type=%s", scope.Type)
	}
}

func TestDataScopeSelf(t *testing.T) {
	scope, err := NewDataScope("file", DataScopeSelf, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := scope.Validate(ChannelAdmin); err != nil {
		t.Fatalf("self scope must be allowed for admin: %v", err)
	}
	if err := scope.Validate(ChannelBiz); err != nil {
		t.Fatalf("self scope must be allowed for biz: %v", err)
	}
}

func TestMergeDataScopes(t *testing.T) {
	mustScope := func(t *testing.T, resource string, scopeType DataScopeType, ids ...string) DataScope {
		t.Helper()
		scope, err := NewDataScope(resource, scopeType, ids)
		if err != nil {
			t.Fatalf("build scope: %v", err)
		}
		return scope
	}

	t.Run("all wins", func(t *testing.T) {
		merged, err := MergeDataScopes([]DataScope{
			mustScope(t, "order", DataScopeMerchant),
			mustScope(t, "order", DataScopeAll),
			mustScope(t, "order", DataScopeMerchantSet, "m1"),
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(merged) != 1 || merged[0].Type != DataScopeAll {
			t.Fatalf("merged=%+v", merged)
		}
	})

	t.Run("merchant beats self", func(t *testing.T) {
		merged, err := MergeDataScopes([]DataScope{
			mustScope(t, "order", DataScopeSelf),
			mustScope(t, "order", DataScopeMerchant),
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(merged) != 1 || merged[0].Type != DataScopeMerchant {
			t.Fatalf("merged=%+v", merged)
		}
	})

	t.Run("merchant set unions ids", func(t *testing.T) {
		merged, err := MergeDataScopes([]DataScope{
			mustScope(t, "order", DataScopeMerchantSet, "m2", "m1"),
			mustScope(t, "order", DataScopeMerchantSet, "m3", "m1"),
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(merged) != 1 || merged[0].Type != DataScopeMerchantSet {
			t.Fatalf("merged=%+v", merged)
		}
		want := []string{"m1", "m2", "m3"}
		if len(merged[0].MerchantIDs) != len(want) {
			t.Fatalf("ids=%v", merged[0].MerchantIDs)
		}
		for i := range want {
			if merged[0].MerchantIDs[i] != want[i] {
				t.Fatalf("ids=%v, want %v", merged[0].MerchantIDs, want)
			}
		}
	})

	t.Run("merchant set beats merchant", func(t *testing.T) {
		merged, err := MergeDataScopes([]DataScope{
			mustScope(t, "order", DataScopeMerchant),
			mustScope(t, "order", DataScopeMerchantSet, "m1"),
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(merged) != 1 || merged[0].Type != DataScopeMerchantSet {
			t.Fatalf("merged=%+v", merged)
		}
		if len(merged[0].MerchantIDs) != 1 {
			t.Fatalf("ids must be preserved, got %v", merged[0].MerchantIDs)
		}
	})

	t.Run("distinct resources sorted", func(t *testing.T) {
		merged, err := MergeDataScopes([]DataScope{
			mustScope(t, "order", DataScopeMerchant),
			mustScope(t, "file", DataScopeSelf),
		})
		if err != nil {
			t.Fatal(err)
		}
		if len(merged) != 2 || merged[0].Resource != "file" || merged[1].Resource != "order" {
			t.Fatalf("merged=%+v", merged)
		}
	})

	t.Run("empty input", func(t *testing.T) {
		merged, err := MergeDataScopes(nil)
		if err != nil {
			t.Fatal(err)
		}
		if len(merged) != 0 {
			t.Fatalf("merged=%+v", merged)
		}
	})

	t.Run("invalid input rejected", func(t *testing.T) {
		if _, err := MergeDataScopes([]DataScope{{Resource: "", Type: DataScopeAll}}); !errors.Is(err, ErrInvalidDataScope) {
			t.Fatalf("err=%v, want ErrInvalidDataScope", err)
		}
	})

	t.Run("cap exceeded", func(t *testing.T) {
		first := make([]string, 0, 60)
		for i := 0; i < 60; i++ {
			first = append(first, "a"+strconv.Itoa(i))
		}
		second := make([]string, 0, 60)
		for i := 0; i < 60; i++ {
			second = append(second, "b"+strconv.Itoa(i))
		}
		_, err := MergeDataScopes([]DataScope{
			mustScope(t, "order", DataScopeMerchantSet, first...),
			mustScope(t, "order", DataScopeMerchantSet, second...),
		})
		if !errors.Is(err, ErrMerchantSetTooLarge) {
			t.Fatalf("err=%v, want ErrMerchantSetTooLarge", err)
		}
	})
}

// itoa 已由 strconv.Itoa 替代，此处不再保留本地实现。
