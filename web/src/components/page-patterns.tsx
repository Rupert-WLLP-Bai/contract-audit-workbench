// File metadata (manifest mirror):
// Purpose: Provides thin, domain-neutral page compositions for lists, object details, version editors and run inspection; data loading, authorization and business decisions stay in their feature pages.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-12
// Updated: 2026-11-12
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import type { FormEventHandler, ReactNode, Ref } from "react";
import "./page-patterns.css";

function classNames(...values: (string | undefined)[]): string {
  return values.filter((value): value is string => Boolean(value)).join(" ");
}

function PageHeading({
  title,
  description,
  eyebrow,
  meta,
  status,
  actions,
  className,
}: {
  title: ReactNode;
  description?: ReactNode;
  eyebrow?: ReactNode;
  meta?: ReactNode;
  status?: ReactNode;
  actions?: ReactNode;
  className?: string;
}) {
  return (
    <header className={classNames("page-head", "page-pattern-heading", className)}>
      <div className="page-pattern-heading-main">
        {eyebrow ? <div className="page-pattern-eyebrow">{eyebrow}</div> : null}
        <h1>{title}</h1>
        {description ? <p>{description}</p> : null}
        {status ? <div className="page-pattern-status">{status}</div> : null}
        {meta ? <div className="page-pattern-meta">{meta}</div> : null}
      </div>
      {actions ? <div className="page-pattern-actions">{actions}</div> : null}
    </header>
  );
}

type PagePatternBaseProps = {
  title: ReactNode;
  description?: ReactNode;
  actions?: ReactNode;
  className?: string;
  contentRef?: Ref<HTMLElement>;
  children: ReactNode;
};

export function ListPage({
  title,
  description,
  actions,
  filters,
  summary,
  pagination,
  className,
  contentRef,
  children,
}: PagePatternBaseProps & {
  filters?: ReactNode;
  summary?: ReactNode;
  pagination?: ReactNode;
}) {
  return (
    <section ref={contentRef} className={classNames("page", "page-pattern-list", className)}>
      <PageHeading title={title} description={description} actions={actions} />
      {summary ? <div className="page-pattern-summary">{summary}</div> : null}
      {filters ? <div className="page-pattern-filters">{filters}</div> : null}
      {children}
      {pagination}
    </section>
  );
}

export function ObjectDetailPage({
  title,
  description,
  actions,
  status,
  facts,
  nextStep,
  before,
  navigation,
  className,
  contentRef,
  children,
}: PagePatternBaseProps & {
  status?: ReactNode;
  facts?: ReactNode;
  nextStep?: ReactNode;
  before?: ReactNode;
  navigation?: ReactNode;
}) {
  return (
    <section ref={contentRef} className={classNames("page", "page-pattern-object", className)}>
      {before}
      <PageHeading title={title} description={description} actions={actions} status={status} />
      {facts}
      {nextStep}
      {navigation}
      <div className="page-pattern-object-content">{children}</div>
    </section>
  );
}

export function VersionEditorPage({
  title,
  description,
  before,
  actions,
  className,
  formClassName,
  onSubmit,
  children,
}: Omit<PagePatternBaseProps, "contentRef"> & {
  before?: ReactNode;
  formClassName?: string;
  onSubmit: FormEventHandler<HTMLFormElement>;
}) {
  return (
    <section className={classNames("page", "page-pattern-version-editor", className)}>
      {before}
      <PageHeading title={title} description={description} />
      <form
        className={classNames("page-pattern-form", formClassName)}
        noValidate
        onSubmit={onSubmit}
      >
        {children}
        {actions ? <div className="page-pattern-form-actions">{actions}</div> : null}
      </form>
    </section>
  );
}

export function RunInspectionPage({
  title,
  description,
  eyebrow,
  meta,
  actions,
  status,
  className,
  headingClassName,
  children,
}: PagePatternBaseProps & {
  eyebrow?: ReactNode;
  meta?: ReactNode;
  status?: ReactNode;
  headingClassName?: string;
}) {
  return (
    <section className={classNames("page", "page-pattern-run-inspection", className)}>
      <PageHeading
        title={title}
        description={description}
        eyebrow={eyebrow}
        meta={meta}
        status={status}
        actions={actions}
        className={headingClassName}
      />
      <div className="page-pattern-run-content">{children}</div>
    </section>
  );
}
