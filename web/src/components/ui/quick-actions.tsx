import type { ReactNode } from "react";
import { Link } from "react-router";

export type QuickActionItem = {
  to: string;
  icon: ReactNode;
  title: string;
  description: string;
  disabled?: boolean;
  disabledReason?: string;
};

function ActionBody({ item }: { item: QuickActionItem }) {
  return (
    <>
      <span className="caw-quick-actions__icon" aria-hidden="true">
        {item.icon}
      </span>
      <strong>{item.title}</strong>
      <span>{item.description}</span>
    </>
  );
}

export function QuickActions({
  items,
  ariaLabel = "快捷操作",
}: {
  items: QuickActionItem[];
  ariaLabel?: string;
}) {
  return (
    <div className="caw-quick-actions" aria-label={ariaLabel}>
      {items.map((item) =>
        item.disabled ? (
          <span
            key={item.to}
            className="caw-quick-actions__item caw-quick-actions__item--disabled"
            aria-disabled="true"
            title={item.disabledReason ?? "当前不可用"}
          >
            <ActionBody item={item} />
          </span>
        ) : (
          <Link key={item.to} className="caw-quick-actions__item" to={item.to}>
            <ActionBody item={item} />
          </Link>
        ),
      )}
    </div>
  );
}
