// File metadata (manifest mirror):
// Purpose: Provides one shared disclosure/anchor pattern for long evidence pages so dense secondary sections do not flatten into an unreadable single scroll.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-04
// Updated: 2026-11-05
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

import { useCallback, useEffect, useId, useRef, useState, type ReactNode } from "react";
import { useNavigableSection, useSectionNavigationAvailable } from "./section-navigation";

export interface SectionAnchor {
  id: string;
  label: string;
  count?: number;
}

/**
 * Collapsible section with a stable DOM id. `defaultOpen` keeps primary
 * conclusions visible; dense supporting sections are opt-in.
 */
export function CollapsibleSection({
  id,
  title,
  count,
  meta,
  defaultOpen = false,
  openRequest,
  summaryLabel,
  children,
}: {
  id: string;
  title: string;
  count?: number;
  /** A compact conclusion shown in the collapsed header, so the primary
   *  state stays visible even when the detail body is collapsed. */
  meta?: ReactNode;
  defaultOpen?: boolean;
  /** A changed request opens this section without remounting its contents. */
  openRequest?: string | number;
  /** Used when the inner content already has a heading with `title`, to avoid
   *  duplicate text matches in assistive technology and tests. */
  summaryLabel?: string;
  children: ReactNode;
}) {
  const generatedId = useId();
  const panelId = `${id}-${generatedId}`;
  const [open, setOpen] = useState(defaultOpen);
  const sectionRef = useRef<HTMLDetailsElement>(null);
  const openSection = useCallback(() => setOpen(true), []);
  useNavigableSection(sectionRef, openSection);
  useEffect(() => { if (openRequest !== undefined) setOpen(true); }, [openRequest]);
  return (
    <details
      ref={sectionRef}
      className="caw-section"
      id={id}
      open={open}
      onToggle={(event) => setOpen(event.currentTarget.open)}
    >
      <summary className="caw-section-summary">
        <span className="caw-section-title">
          {open ? title : (summaryLabel ?? title)}
        </span>
        <span className="caw-section-summary-meta">
          {meta === undefined ? null : <span className="caw-section-meta">{meta}</span>}
          {count === undefined ? null : (
            <span className="caw-section-count" aria-label={`${count} 项`}>
              {count} 项
            </span>
          )}
        </span>
      </summary>
      <div className="caw-section-body" id={panelId}>
        {children}
      </div>
    </details>
  );
}

/** In-page jump links; opening a collapsed target is handled here because
 *  browsers do not consistently reveal a closed <details> from a fragment
 *  navigation. Keyboard activation uses the same click path. */
export function SectionAnchorNav({ items }: { items: SectionAnchor[] }) {
  const coordinated = useSectionNavigationAvailable();
  if (items.length < 2) return null;
  return (
    <nav className="caw-section-nav" aria-label="页面分区">
      {items.map((item) => (
        <a
          key={item.id}
          href={`#${item.id}`}
          aria-label={`跳转到${item.label}`}
          onClick={() => {
            if (coordinated) return;
            const target = document.getElementById(item.id);
            if (target instanceof HTMLDetailsElement) target.open = true;
          }}
        >
          <span aria-hidden="true">↳</span> {item.label}
          {item.count === undefined ? null : <span> · {item.count}</span>}
        </a>
      ))}
    </nav>
  );
}
