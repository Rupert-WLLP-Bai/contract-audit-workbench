package rulepack

import (
	"context"
	"errors"
	"testing"
)

func TestExecutionRejectsMalformedFactAndSourceIdentity(t *testing.T) {
	pack := demoPublished(t)
	cases := []struct {
		name   string
		mutate func(*Request)
		want   error
	}{
		{"duplicate-source", func(request *Request) { request.Sources = append(request.Sources, request.Sources[0]) }, ErrIdentity},
		{"empty-source-id", func(request *Request) { request.Sources[0].ID = "" }, ErrIdentity},
		{"wrong-citation-source", func(request *Request) { request.Facts[1].Citations[0].SourceID = "absent" }, ErrIdentity},
		{"negative-codepoint-start", func(request *Request) { request.Facts[1].Citations[0].Start = -1 }, ErrIdentity},
		{"empty-codepoint-span", func(request *Request) { request.Facts[1].Citations[0].End = 0 }, ErrIdentity},
		{"unknown-rule", func(request *Request) { request.Facts[0].RuleID = "missing-rule" }, ErrInvalidRequest},
		{"duplicate-fact-id", func(request *Request) { request.Facts[1].ID = request.Facts[0].ID }, ErrInvalidRequest},
		{"empty-fact-id", func(request *Request) { request.Facts[1].ID = "" }, ErrInvalidRequest},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			request := demoRequest(t)
			test.mutate(&request)
			if _, err := runRequest(t, pack, request); !errors.Is(err, test.want) {
				t.Fatalf("execution error %v, want %v", err, test.want)
			}
		})
	}
}

func TestExecutionConservativelyAbstainsOnUnsupportedValuesAndRoles(t *testing.T) {
	pack := demoPublished(t)
	cases := []struct {
		name   string
		mutate func(*Request)
		factID string
		ruleID string
	}{
		{"bad-rational", func(request *Request) { request.Facts[1].Value = "not-a-number" }, "fact-bandwidth", "ENT-TECH-BW-001"},
		{"wrong-bandwidth-unit", func(request *Request) { request.Facts[1].Unit = "MiB/s" }, "fact-bandwidth", "ENT-TECH-BW-001"},
		{"source-role-disagrees", func(request *Request) { request.Sources[0].Role = "award" }, "fact-bandwidth", "ENT-TECH-BW-001"},
		{"metadata-object-missing", func(request *Request) { request.Facts[1].ObjectRef = "" }, "fact-bandwidth", "ENT-TECH-BW-001"},
		{"availability-over-100", func(request *Request) {
			replaceEvidence(t, request, "fact-sla", "专线甲自然月可用率为120%，时区Asia/Shanghai，日历Gregorian，故障口径all_service_unavailability，无排除项。", "120", "percent", "专线甲")
		}, "fact-sla", "ENT-TECH-SLA-001"},
		{"advance-ratio-over-one", func(request *Request) {
			replaceEvidence(t, request, "fact-advance", "专线甲合同总额为预付款计算基础，预付款比例为1.1。", "1.1", "ratio", "专线甲")
		}, "fact-advance", "ENT-COM-ADV-001"},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			request := demoRequest(t)
			test.mutate(&request)
			report, err := runRequest(t, pack, request)
			if err != nil {
				t.Fatal(err)
			}
			if got := resultFor(t, report, test.ruleID).Relation; got != RelationInsufficient {
				t.Fatalf("%s relation %s", test.factID, got)
			}
		})
	}
}

func TestExecutionRejectsNilPack(t *testing.T) {
	var pack *Pack
	if _, err := pack.Execute(context.Background(), demoRequest(t)); !errors.Is(err, ErrInvalidManifest) {
		t.Fatalf("nil pack execution error %v", err)
	}
}

type cancelAfterPreflightContext struct {
	context.Context
	cancel context.CancelFunc
	checks int
}

func (ctx *cancelAfterPreflightContext) Err() error {
	if ctx.checks > 0 {
		ctx.cancel()
	}
	ctx.checks++
	return ctx.Context.Err()
}

func TestExecutionStopsWhenContextIsCanceledDuringRun(t *testing.T) {
	base, cancel := context.WithCancel(context.Background())
	defer cancel()
	ctx := &cancelAfterPreflightContext{Context: base, cancel: cancel}
	if _, err := demoPublished(t).Execute(ctx, demoRequest(t)); !errors.Is(err, context.Canceled) {
		t.Fatalf("mid-run cancellation error %v", err)
	}
}
