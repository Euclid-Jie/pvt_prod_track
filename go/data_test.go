package main

import (
	"strings"
	"testing"
)

func TestManagerScaleQueryUsesOnlyPrivateManagerProfile(t *testing.T) {
	if !strings.Contains(managerScaleQuery, "FROM `private_manager_profile`") {
		t.Fatalf("manager scales must come from private_manager_profile: %s", managerScaleQuery)
	}
}

func TestSourceQueriesDoNotCarryScale(t *testing.T) {
	queries := map[string]string{
		"fund_basic_info": fundBasicInfoQuery,
		"alpha_plus":      alphaPlusFundQuery,
	}
	for name, query := range queries {
		for _, forbidden := range []string{"private_manager_profile", "company_scale", "管理人规模", "管理规模"} {
			if strings.Contains(query, forbidden) {
				t.Fatalf("%s query must not read or join scale itself (%q): %s", name, forbidden, query)
			}
		}
		if !strings.Contains(query, "COALESCE(comp_code, '')") {
			t.Fatalf("%s query must expose comp_code for the scale lookup: %s", name, query)
		}
	}
}

func TestManagerScaleKeyNormalizesCompCode(t *testing.T) {
	if got := managerScaleKey(" p1028099 "); got != "P1028099" {
		t.Fatalf("manager scale lookup key must trim whitespace and ignore case: %q", got)
	}
}

func TestFillManagerScalesUsesOnlyPrivateManagerProfile(t *testing.T) {
	infos := []fundInfo{
		{ProdCode: "exact", CompCode: "P1028099"},
		{ProdCode: "padded case", CompCode: " p1028099 "},
		{ProdCode: "no comp_code"},
		{ProdCode: "unknown manager", CompCode: "P9999999"},
		{ProdCode: "stale scale", CompCode: "P1028099", Scale: "stale"},
	}

	fillManagerScales(infos, map[string]string{"P1028099": "10-20亿元"})

	want := []string{"10-20亿元", "10-20亿元", "", "", "10-20亿元"}
	for i := range infos {
		if infos[i].Scale != want[i] {
			t.Fatalf("%s scale = %q, want %q", infos[i].ProdCode, infos[i].Scale, want[i])
		}
	}
}

func TestExcludeBackupFundInfosUsesStandardMetricCode(t *testing.T) {
	infos := []fundInfo{
		{ProdCode: "SXN345", ProdName: "primary"},
		{ProdCode: "SBQS25", ProdName: "backup", MetricCode: "fof99:SBQS25"},
		{ProdCode: "AP-A", ProdName: "alpha plus", MetricCode: "alpha_plus:AP-A"},
		{ProdCode: "MAIL-A", ProdName: "mail", MetricCode: "mail:MAIL-A"},
	}

	filtered := excludeBackupFundInfos(
		infos,
		map[string]struct{}{"fof99:SBQS25": {}, "alpha_plus:AP-A": {}, "mail:MAIL-A": {}},
	)

	if len(filtered) != 1 {
		t.Fatalf("expected one non-backup fund, got %d", len(filtered))
	}
	if filtered[0].ProdCode != "SXN345" {
		t.Fatalf("expected SXN345 to remain, got %s", filtered[0].ProdCode)
	}
}
