package rulepack

import "testing"

func TestEvidenceSupportsAuthorityDirectionalBounds(t *testing.T) {
	const slaQuote = "云服务 CLOUD-001：SLA 可用率至少99.95%；统计周期：自然月；时区 Asia/Shanghai；公历 gregorian；故障口径 all_service_unavailability；无排除项。"
	const advanceQuote = "云服务 CLOUD-001：审批依据，预付款上限不得超过30%，预付款以合同总额为基础，含税。"
	tests := []struct {
		name, comparator, role, object, quote, value, unit string
		window, timeZone, calendar, outage, basis          string
		exclusions                                         []string
		wantAuthority                                      bool
	}{
		{
			name: "minimum bandwidth accepts a lower bound", comparator: ComparatorMinimumBandwidth,
			role: "procurement", quote: "云服务 CLOUD-001：最低保障带宽不低于100 Mbit/s。", value: "100", unit: "Mbit/s",
			wantAuthority: true,
		},
		{
			name: "minimum bandwidth rejects an upper bound", comparator: ComparatorMinimumBandwidth,
			role: "procurement", quote: "云服务 CLOUD-001：最低保障带宽不超过100 Mbit/s。", value: "100", unit: "Mbit/s",
		},
		{
			name: "minimum bandwidth rejects a different value", comparator: ComparatorMinimumBandwidth,
			role: "procurement", quote: "云服务 CLOUD-001：最低保障带宽不低于100 Mbit/s。", value: "101", unit: "Mbit/s",
		},
		{
			name: "minimum bandwidth rejects a different object", comparator: ComparatorMinimumBandwidth,
			role: "procurement", object: "CLOUD-002", quote: "云服务 CLOUD-001：最低保障带宽不低于100 Mbit/s。", value: "100", unit: "Mbit/s",
		},
		{
			name: "minimum bandwidth rejects an unsupported unit", comparator: ComparatorMinimumBandwidth,
			role: "procurement", quote: "云服务 CLOUD-001：最低保障带宽不低于100 Mbit/s。", value: "100", unit: "MiB/s",
		},
		{
			name: "monthly SLA rejects a mismatched time zone", comparator: ComparatorMonthlySLA,
			role: "procurement", quote: slaQuote, value: "99.95", unit: "percent",
			window: "calendar_month", timeZone: "UTC", calendar: "gregorian",
			outage: "all_service_unavailability", exclusions: []string{},
		},
		{
			name: "advance ceiling requires contract total basis evidence", comparator: ComparatorAdvancePaymentCeiling,
			role: "award", quote: "云服务 CLOUD-001：审批依据，预付款上限不得超过30%。", value: "3/10", unit: "ratio", basis: "contract_total",
		},
		{
			name: "advance ceiling rejects an unsupported unit", comparator: ComparatorAdvancePaymentCeiling,
			role: "award", quote: advanceQuote, value: "3/10", unit: "percent", basis: "contract_total",
		},
		{
			name: "monthly SLA accepts a lower bound", comparator: ComparatorMonthlySLA,
			role: "award", quote: slaQuote, value: "99.95", unit: "percent",
			window: "calendar_month", timeZone: "Asia/Shanghai", calendar: "gregorian",
			outage: "all_service_unavailability", exclusions: []string{}, wantAuthority: true,
		},
		{
			name: "monthly SLA rejects an upper bound", comparator: ComparatorMonthlySLA,
			role: "procurement", quote: "云服务 CLOUD-001：SLA 可用率不高于99.95%；统计周期：自然月；时区 Asia/Shanghai；公历 gregorian；故障口径 all_service_unavailability；无排除项。", value: "99.95", unit: "percent",
			window: "calendar_month", timeZone: "Asia/Shanghai", calendar: "gregorian",
			outage: "all_service_unavailability", exclusions: []string{},
		},
		{
			name: "advance ceiling accepts an upper bound", comparator: ComparatorAdvancePaymentCeiling,
			role: "award", quote: advanceQuote, value: "3/10", unit: "ratio", basis: "contract_total",
			wantAuthority: true,
		},
		{
			name: "advance ceiling rejects a lower bound", comparator: ComparatorAdvancePaymentCeiling,
			role: "award", quote: "云服务 CLOUD-001：审批依据，预付款上限不少于30%，预付款以合同总额为基础，含税。", value: "3/10", unit: "ratio", basis: "contract_total",
		},
		{
			name: "negative lower bound remains unsupported", comparator: ComparatorMonthlySLA,
			role: "procurement", quote: "云服务 CLOUD-001：并非SLA可用率不低于99.95%；统计周期：自然月；时区 Asia/Shanghai；公历 gregorian；故障口径 all_service_unavailability；无排除项。", value: "99.95", unit: "percent",
			window: "calendar_month", timeZone: "Asia/Shanghai", calendar: "gregorian",
			outage: "all_service_unavailability", exclusions: []string{},
		},
		{
			name: "bounded measured fact is not authority evidence", comparator: ComparatorMinimumBandwidth,
			role: "draft_contract", quote: "云服务 CLOUD-001：最低保障带宽至少100 Mbit/s。", value: "100", unit: "Mbit/s",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			object := test.object
			if object == "" {
				object = "CLOUD-001"
			}
			fact := Fact{
				ObjectRef: object, Value: test.value, Unit: test.unit, SourceRole: test.role,
				MeasurementWindow: test.window, TimeZone: test.timeZone, Calendar: test.calendar,
				OutageDefinition: test.outage, Exclusions: test.exclusions, Basis: test.basis,
				Citations: []Citation{{Quote: test.quote}},
			}
			rule := Rule{Comparator: test.comparator}
			if got := EvidenceSupportsAuthority(rule, fact); got != test.wantAuthority {
				t.Fatalf("EvidenceSupportsAuthority = %t, want %t", got, test.wantAuthority)
			}
			if got := EvidenceSupportsValue(rule, fact); got {
				t.Fatal("EvidenceSupportsValue accepted a bound as a measured value")
			}
		})
	}
}

func TestMonthlySLAExclusionsThroughPublicEvidenceEntryPoint(t *testing.T) {
	const prefix = "云服务 CLOUD-001：SLA 可用率至少99.95%；统计周期：自然月；时区 Asia/Shanghai；公历 gregorian；故障口径 all_service_unavailability；"
	tests := []struct {
		name       string
		quote      string
		exclusions []string
		want       bool
	}{
		{name: "no exclusions", quote: prefix + "无排除项。", exclusions: []string{}, want: true},
		{name: "listed exclusions sorted", quote: prefix + "排除项：force_majeure、planned_maintenance。", exclusions: []string{"planned_maintenance", "force_majeure"}, want: true},
		{name: "missing declaration", quote: prefix, exclusions: []string{}},
		{name: "no exclusions conflicts with list", quote: prefix + "无排除项；排除项：planned_maintenance。", exclusions: []string{"planned_maintenance"}},
		{name: "duplicate list markers", quote: prefix + "排除项：planned_maintenance；排除项：planned_maintenance。", exclusions: []string{"planned_maintenance"}},
		{name: "wrong list", quote: prefix + "排除项：planned_maintenance。", exclusions: []string{"force_majeure"}},
		{name: "additional exception language", quote: prefix + "排除项：planned_maintenance，其他例外另行约定。", exclusions: []string{"planned_maintenance"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fact := Fact{
				ObjectRef: "CLOUD-001", Value: "99.95", Unit: "percent", SourceRole: "award",
				MeasurementWindow: "calendar_month", TimeZone: "Asia/Shanghai", Calendar: "gregorian",
				OutageDefinition: "all_service_unavailability", Exclusions: test.exclusions,
				Citations: []Citation{{Quote: test.quote}},
			}
			if got := EvidenceSupportsAuthority(Rule{Comparator: ComparatorMonthlySLA}, fact); got != test.want {
				t.Fatalf("EvidenceSupportsAuthority = %t, want %t", got, test.want)
			}
		})
	}
}

func TestEvidenceSupportsValueFoldsSoftBreakInsideBandwidthToken(t *testing.T) {
	fact := Fact{
		ObjectRef: "CLOUD-001", Value: "1/2", Unit: "Mbit/s", Dimension: "bandwidth",
		SourceRole: "award", Citations: []Citation{{Quote: "云服务 CLOUD-001：带宽为1\n/2 Mbit/s。"}},
	}
	if !EvidenceSupportsValue(Rule{Comparator: ComparatorMinimumBandwidth}, fact) {
		t.Fatal("EvidenceSupportsValue rejected a rational split by a soft line break")
	}
}
