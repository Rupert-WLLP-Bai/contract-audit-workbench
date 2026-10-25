package telecom_test

import (
	"context"
	"fmt"
	"testing"

	"contractaudit/internal/rulepack"
	"contractaudit/internal/telecom"
)

func TestWrappedQualifiersCannotBecomeCompatibleEvidence(t *testing.T) {
	for _, newline := range []string{"\n", "\r\n"} {
		for _, qualifier := range []string{"未达到", "不满足", "没有承诺"} {
			t.Run(fmt.Sprintf("%s-%q", qualifier, newline), func(t *testing.T) {
				req := request("100 Mbit/s", "99.9%", "20%")
				replaceSource(&req, "draft", "电路 LINE-001：最低保障带宽 100 Mbit/s。", qualifier+newline+"电路 LINE-001：最低保障带宽 100 Mbit/s。")
				report, err := telecom.Execute(context.Background(), req)
				if err != nil {
					t.Fatal(err)
				}
				got := result(t, report, "ENT-TECH-BW-001")
				if got.Relation != rulepack.RelationInsufficient {
					t.Fatalf("negated wrapped promise became %s (%s)", got.Relation, got.ReasonCode)
				}
			})
		}
	}
}
