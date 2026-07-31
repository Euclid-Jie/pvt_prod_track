package main

import "testing"

func TestExcludeBackupFundInfosUsesStandardMetricCode(t *testing.T) {
	infos := []fundInfo{
		{ProdCode: "SXN345", ProdName: "primary"},
		{ProdCode: "SBQS25", ProdName: "backup", MetricCode: "fof99:SBQS25"},
	}

	filtered := excludeBackupFundInfos(
		infos,
		map[string]struct{}{"fof99:SBQS25": {}},
	)

	if len(filtered) != 1 {
		t.Fatalf("expected one non-backup fund, got %d", len(filtered))
	}
	if filtered[0].ProdCode != "SXN345" {
		t.Fatalf("expected SXN345 to remain, got %s", filtered[0].ProdCode)
	}
}
