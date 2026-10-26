import { Alert, Button, Empty, Skeleton } from "antd";
import type { ReactNode } from "react";

export function EmptyState({
  title,
  detail,
  action,
}: {
  title: ReactNode;
  detail?: ReactNode;
  action?: ReactNode;
}) {
  return (
    <section className="caw-state" role="status">
      <Empty
        image={Empty.PRESENTED_IMAGE_SIMPLE}
        description={
          <div>
            <h2 className="caw-state__title">{title}</h2>
            {detail ? <p className="caw-state__detail">{detail}</p> : null}
          </div>
        }
      />
      {action}
    </section>
  );
}

export function ErrorState({
  title,
  detail,
  action,
}: {
  title: ReactNode;
  detail?: ReactNode;
  action?: ReactNode;
}) {
  return (
    <div className="caw-error-state">
      <Alert
        type="error"
        showIcon
        title={title}
        description={detail}
        action={action}
      />
    </div>
  );
}

export function RetryAction({
  onRetry,
  label = "重试",
}: {
  onRetry: () => void;
  label?: string;
}) {
  return (
    <Button type="primary" htmlType="button" onClick={onRetry}>
      {label}
    </Button>
  );
}

export function LoadingBlock({ label = "正在加载" }: { label?: string }) {
  return (
    <div
      className="caw-loading-block"
      role="status"
      aria-live="polite"
      aria-busy="true"
    >
      <span className="sr-only">{label}</span>
      <Skeleton active paragraph={{ rows: 4 }} title={{ width: "32%" }} />
    </div>
  );
}
