// File metadata (manifest mirror):
// Purpose: Provides legal-review navigation to verified frozen contract clauses and bounded source fragments without creating risk conclusions.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-12
// Updated: 2026-11-12
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { Button } from "antd";
import {
  keyClauseCategoryLabel,
  type KeyClauseCandidate,
  type KeyClauseNavigation,
} from "../domain/key-clauses";

export function KeyClauseNavigator({
  navigation,
  selectedId,
  onSelect,
}: {
  navigation: KeyClauseNavigation;
  selectedId: string | null;
  onSelect: (candidate: KeyClauseCandidate) => void;
}) {
  const selected = navigation.candidates.find((candidate) => candidate.id === selectedId) ?? null;
  return (
    <section className="key-clause-navigator" aria-label="关键条款导航">
      <h3>关键条款导航</h3>
      <p className="meta">仅从当前冻结审查尝试的解析材料读取已保存条款。</p>
      {navigation.candidates.length === 0 ? (
        <p className="meta" role="status">
          {navigation.message}
        </p>
      ) : (
        <ul className="key-clause-list">
          {navigation.candidates.map((candidate) => (
            <li key={candidate.id}>
              <Button
                className="key-clause-choice"
                htmlType="button"
                aria-pressed={selectedId === candidate.id}
                aria-label={`${candidate.heading}，${candidate.sourceLabel}，${candidate.categories
                  .map(keyClauseCategoryLabel)
                  .join("、")}${
                  candidate.page === null ? "，页码未记录" : `，起始页第 ${candidate.page} 页`
                }`}
                data-document-version-id={candidate.documentId}
                data-parsed-document-id={candidate.parsedDocumentId}
                onClick={() => onSelect(candidate)}
              >
                <span>{candidate.heading}</span>
                <span className="key-clause-source">{candidate.sourceLabel}</span>
                <span className="key-clause-categories">
                  {candidate.categories.map(keyClauseCategoryLabel).join(" · ")}
                </span>
                {candidate.page === null ? (
                  <span className="key-clause-page">页码未记录</span>
                ) : (
                  <span className="key-clause-page">起始页：第 {candidate.page} 页</span>
                )}
              </Button>
            </li>
          ))}
        </ul>
      )}
      {navigation.notice ? (
        <p className="meta" role="status">
          {navigation.notice}
        </p>
      ) : null}
      {selected ? (
        <p className="key-clause-selection" role="status">
          已在冻结文字中定位“{selected.heading}”；
          {selected.kind === "fragment"
            ? "这是正文片段，不是已解析的完整结构化条款；原件页码未记录，只定位冻结文字。"
            : selected.page === null
              ? "原件页码未记录，只定位冻结文字。"
              : `ClauseNode 记录的起始页为第 ${selected.page} 页。`}
        </p>
      ) : null}
      <p className="meta">
        关键词分类只用于查找，不表示风险判断；正文片段不是结构化条款，覆盖有限，可能遗漏需要人工关注的条款。
      </p>
    </section>
  );
}
