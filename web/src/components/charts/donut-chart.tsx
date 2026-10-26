import type { ReactNode } from "react";
import "./charts.css";

export type DonutItem = {
  key: string;
  label: string;
  value: number;
  color: string;
};

export function DonutChart({
  items,
  centerValue,
  centerLabel,
  ariaLabel,
  size = 190,
}: {
  items: DonutItem[];
  centerValue: ReactNode;
  centerLabel: ReactNode;
  ariaLabel: string;
  size?: number;
}) {
  const total = items.reduce((sum, item) => sum + item.value, 0);
  const radius = 66;
  const circumference = 2 * Math.PI * radius;
  let offset = 0;
  return (
    <div
      className="caw-donut"
      style={{ width: size, height: size }}
      role="img"
      aria-label={ariaLabel}
    >
      <svg viewBox="0 0 180 180" aria-hidden="true">
        <circle
          cx="90"
          cy="90"
          r={radius}
          fill="none"
          stroke="#EEF2F7"
          strokeWidth="18"
        />
        {items
          .filter((item) => item.value > 0)
          .map((item) => {
            const length = (item.value / total) * circumference;
            const visible = Math.max(0, length - 3);
            const element = (
              <circle
                key={item.key}
                cx="90"
                cy="90"
                r={radius}
                fill="none"
                stroke={item.color}
                strokeWidth="18"
                strokeDasharray={`${visible} ${circumference - visible}`}
                strokeDashoffset={-offset}
                transform="rotate(-90 90 90)"
              >
                <title>
                  {item.label}：{item.value}
                </title>
              </circle>
            );
            offset += length;
            return element;
          })}
      </svg>
      <span className="caw-donut__center" aria-hidden="true">
        <strong>{centerValue}</strong>
        <span>{centerLabel}</span>
      </span>
    </div>
  );
}

export function ChartLegend({
  items,
  total,
  showPercent = true,
}: {
  items: DonutItem[];
  total: number;
  showPercent?: boolean;
}) {
  return (
    <ul className="caw-chart-legend">
      {items.map((item) => (
        <li key={item.key}>
          <span
            className="caw-chart-legend__dot"
            style={{ background: item.color }}
            aria-hidden="true"
          />
          <span className="caw-chart-legend__label">{item.label}</span>
          <span className="caw-chart-legend__value">{item.value}</span>
          {showPercent ? (
            <span className="caw-chart-legend__percent">
              {total === 0 ? "0%" : `${Math.round((item.value / total) * 100)}%`}
            </span>
          ) : null}
        </li>
      ))}
    </ul>
  );
}
