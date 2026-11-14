// File metadata (manifest mirror):
// Purpose: Checks exact page attribution for frozen text spans across block and physical page boundaries.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-12
// Updated: 2026-11-12
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

package docir_test

import (
	"contractaudit/internal/docir"
	"testing"
)

func TestBindPageFromObservedPageSpanAcrossBlocks(t *testing.T) {
	tests := []struct {
		name, raw, quote string
		page             int
	}{
		{"same page", "前文\n\n跨段\n\n引用\f后页", "跨段\n\n引用", 1},
		{"across pages", "第一条 内容\f第二页内容", "内容\n\n第二页", 0},
		{"unpaged", "前文\n\n跨段\n\n引用", "跨段\n\n引用", 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ir := docir.Build(tt.raw)
			got := docir.Locate(ir, tt.quote)
			if got.Status != docir.StatusLocated {
				t.Fatalf("not located: %+v", got)
			}
			if tt.page == 0 && got.Page != nil || tt.page != 0 && (got.Page == nil || *got.Page != tt.page) {
				t.Fatalf("page = %v, want %d", got.Page, tt.page)
			}
			if tt.name == "same page" && got.ElementID != "" {
				t.Fatalf("invented element: %s", got.ElementID)
			}
		})
	}
	ir := docir.Build("甲\n\n乙\f丙")
	if got := docir.Bind(ir, "甲", -1, 1); got.Status != docir.StatusUnlocatable || got.Page != nil {
		t.Fatalf("invalid offset: %+v", got)
	}
}
