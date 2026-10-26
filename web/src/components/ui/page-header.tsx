import type { ReactNode } from "react";

export function PageHeader({
  title,
  description,
  eyebrow,
  extra,
}: {
  title: ReactNode;
  description?: ReactNode;
  eyebrow?: ReactNode;
  extra?: ReactNode;
}) {
  return (
    <header className="caw-page-header">
      <div className="caw-page-header__main">
        {eyebrow ? (
          <div className="caw-page-header__eyebrow">{eyebrow}</div>
        ) : null}
        <h1>{title}</h1>
        {description ? (
          <p className="caw-page-header__description">{description}</p>
        ) : null}
      </div>
      {extra ? <div className="caw-page-header__extra">{extra}</div> : null}
    </header>
  );
}
