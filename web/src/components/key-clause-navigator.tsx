// File metadata (manifest mirror):
// Purpose: Provides legal-review navigation to verified frozen contract clauses and bounded source fragments without creating risk conclusions.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-12
// Updated: 2026-11-19
// Revision: r2
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
      <h3>阅读重点</h3>

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
                <span className="key-clause-categories">
                  {candidate.categories.map(keyClauseCategoryLabel).join(" · ")}
                  {candidate.page === null ? null : ` · 起始页：第 ${candidate.page} 页`}
                </span>
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
          已定位“{selected.heading}”。
          {selected.page === null ? "未记录页码。" : `起始页：第 ${selected.page} 页。`}
        </p>
      ) : null}
      <p className="meta">选择后跳到原文。</p>
    </section>
  );
}
