package codebuddy

import "testing"

// TestModelAllowedInRegion 是「两区模型不互通」与「自定义模型不拒绝」两条规则的
// 唯一实现点，必须逐条锁定。
func TestModelAllowedInRegion(t *testing.T) {
	cases := []struct {
		name        string
		accountType string
		model       string
		want        bool
	}{
		{"CN 账号 + CN 独有模型", AccountTypeCN, "deepseek-v4-pro", true},
		{"Global 账号 + CN 独有模型 → 跨区拒绝", AccountTypeGlobal, "deepseek-v4-pro", false},
		{"CN 账号 + 两区共有模型", AccountTypeCN, "hy4-preview-x", true},
		{"Global 账号 + 两区共有模型", AccountTypeGlobal, "hy4-preview-x", true},
		// deepseek-v4.1-flash 是两区共有（Global 表本身是 CN 表的子集）。
		{"CN 账号 + 两区共有的 deepseek-v4.1-flash", AccountTypeCN, "deepseek-v4.1-flash", true},
		{"Global 账号 + 两区共有的 deepseek-v4.1-flash", AccountTypeGlobal, "deepseek-v4.1-flash", true},
		{"CN 账号 + 未知/自定义模型 → 放行透传", AccountTypeCN, "my-custom-model", true},
		{"Global 账号 + 未知/自定义模型 → 放行透传", AccountTypeGlobal, "my-custom-model", true},
		{"空模型 → 放行", AccountTypeCN, "", true},
		{"空模型 + Global → 放行", AccountTypeGlobal, "", true},
		{"纯空白模型 → 放行", AccountTypeCN, "   ", true},
		{"未知账号类型按 CN 判定", "weird_type", "deepseek-v4-pro", true},
		{"未知账号类型 + 仅 CN 模型 → 放行（回落 CN）", "weird_type", "glm-5.3-flash", true},
		{"大小写归一", AccountTypeCN, "DEEPSEEK-V4-PRO", true},
		{"auto 两区共有", AccountTypeGlobal, "auto", true},
		{"glm-5.3-flash 仅 CN", AccountTypeGlobal, "glm-5.3-flash", false},
		{"kimi-k3-1 仅 CN", AccountTypeGlobal, "kimi-k3-1", false},
		{"kimi-k3-1 CN", AccountTypeCN, "kimi-k3-1", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := ModelAllowedInRegion(c.accountType, c.model); got != c.want {
				t.Errorf("ModelAllowedInRegion(%q,%q)=%v want %v", c.accountType, c.model, got, c.want)
			}
		})
	}
}

func TestModelRegions(t *testing.T) {
	if got := ModelRegions("deepseek-v4-pro"); len(got) != 1 || got[0] != RegionCN {
		t.Errorf("ModelRegions(deepseek-v4-pro)=%v want [cn]", got)
	}
	if got := ModelRegions("hy4-preview-x"); len(got) != 2 {
		t.Errorf("ModelRegions(hy4-preview-x)=%v want 2 区", got)
	}
	if got := ModelRegions("nope"); got != nil {
		t.Errorf("ModelRegions(nope)=%v want nil", got)
	}
}

func TestIsAccountType(t *testing.T) {
	for _, at := range []string{AccountTypeCN, AccountTypeGlobal} {
		if !IsAccountType(at) {
			t.Errorf("IsAccountType(%q)=false want true", at)
		}
	}
	for _, at := range []string{"", "kiro", "codebuddy", "CODEBUDDY_CN"} {
		if IsAccountType(at) {
			t.Errorf("IsAccountType(%q)=true want false", at)
		}
	}
}

func TestRegionForAccountType(t *testing.T) {
	if RegionForAccountType(AccountTypeGlobal) != RegionGlobal {
		t.Error("global account type should map to RegionGlobal")
	}
	if RegionForAccountType(AccountTypeCN) != RegionCN {
		t.Error("cn account type should map to RegionCN")
	}
	if RegionForAccountType("unknown") != RegionCN {
		t.Error("unknown account type should fall back to RegionCN")
	}
}

// TestStaticModelIDs 静态表按区域返回，且 Global 不含 CN 独有的 deepseek-v4-pro。
func TestStaticModelIDs(t *testing.T) {
	cn := StaticModelIDs(RegionCN)
	gl := StaticModelIDs(RegionGlobal)

	if len(cn) == 0 || len(gl) == 0 {
		t.Fatal("static tables must not be empty")
	}
	contains := func(ids []string, want string) bool {
		for _, id := range ids {
			if id == want {
				return true
			}
		}
		return false
	}
	if !contains(cn, "deepseek-v4-pro") {
		t.Error("CN static table should contain deepseek-v4-pro")
	}
	if contains(gl, "deepseek-v4-pro") {
		t.Error("Global static table must NOT contain deepseek-v4-pro (code=11102)")
	}
	if !contains(gl, "deepseek-v4.1-flash") {
		t.Error("Global static table should contain deepseek-v4.1-flash")
	}

	// 返回副本：调用方修改不得污染包内表。
	cn[0] = "mutated"
	if StaticModelIDs(RegionCN)[0] == "mutated" {
		t.Error("StaticModelIDs must return a copy")
	}
}

func TestStaticModelsAll(t *testing.T) {
	all := StaticModelsAll()
	if len(all) == 0 {
		t.Fatal("union must not be empty")
	}
	// 去重 + 升序
	for i := 1; i < len(all); i++ {
		if all[i-1] >= all[i] {
			t.Fatalf("union not deduped/sorted: %v", all)
		}
	}
	// 两区模型都应在并集内
	want := map[string]bool{"deepseek-v4-pro": false, "deepseek-v4.1-flash": false, "auto": false}
	for _, id := range all {
		if _, ok := want[id]; ok {
			want[id] = true
		}
	}
	for id, found := range want {
		if !found {
			t.Errorf("union missing %q", id)
		}
	}
}
