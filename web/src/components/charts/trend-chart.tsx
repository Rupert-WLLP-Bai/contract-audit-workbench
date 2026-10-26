import { useEffect, useRef, useState } from "react";
import { formatDuration, type TrendPoint } from "../../domain/dashboard";
import "./charts.css";

const DEFAULT_WIDTH = 720;
const HEIGHT = 250;
const MARGIN = { top: 18, right: 42, bottom: 30, left: 38 };

function niceMax(value: number): number {
  if (value <= 4) return 4;
  return Math.ceil(value / 4) * 4;
}

export function TrendChart({ points }: { points: TrendPoint[] }) {
  const hostRef = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(DEFAULT_WIDTH);

  useEffect(() => {
    const host = hostRef.current;
    if (!host || typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver((entries) => {
      const measured = Math.round(entries[0]?.contentRect.width ?? 0);
      if (measured > 0) setWidth(measured);
    });
    observer.observe(host);
    return () => observer.disconnect();
  }, []);

  const compact = width < 480;
  const height = compact ? 210 : HEIGHT;
  const plotWidth = width - MARGIN.left - MARGIN.right;
  const plotHeight = height - MARGIN.top - MARGIN.bottom;
  const countMax = niceMax(
    Math.max(0, ...points.map((point) => Math.max(point.started, point.completed))),
  );
  const minutes = points.map((point) =>
    point.averageMs === null ? 0 : point.averageMs / 60000,
  );
  const minuteMax = niceMax(Math.ceil(Math.max(0, ...minutes)));
  const slot = plotWidth / Math.max(1, points.length);
  const barWidth = Math.min(12, Math.max(4, slot * 0.24));
  const hasDuration = points.some((point) => point.averageMs !== null);
  const x = (index: number) => MARGIN.left + slot * index + slot / 2;
  const yCount = (value: number) =>
    MARGIN.top + plotHeight - (value / countMax) * plotHeight;
  const yMinutes = (value: number) =>
    MARGIN.top + plotHeight - (value / minuteMax) * plotHeight;

  let linePath = "";
  points.forEach((point, index) => {
    if (point.averageMs === null) return;
    const command = linePath === "" || points[index - 1]?.averageMs == null ? "M" : "L";
    linePath += `${command}${x(index).toFixed(1)} ${yMinutes(minutes[index]!).toFixed(1)} `;
  });

  return (
    <div className="caw-trend" ref={hostRef}>
      <div className="caw-trend__legend" aria-hidden="true">
        <span>
          <i className="caw-trend__swatch caw-trend__swatch--started" />
          审查数量
        </span>
        <span>
          <i className="caw-trend__swatch caw-trend__swatch--completed" />
          完成数量
        </span>
        {hasDuration ? (
          <span>
            <i className="caw-trend__swatch caw-trend__swatch--line" style={{ background: "#14b8a6" }} />
            平均时长（分钟）
          </span>
        ) : null}
      </div>
      <svg
        viewBox={`0 0 ${width} ${height}`}
        role="img"
        aria-label="近 14 天审查数量、完成数量与平均审查时长趋势"
      >
        {[0, 1, 2, 3, 4].map((tick) => {
          const value = (countMax / 4) * tick;
          const y = yCount(value);
          return (
            <g key={tick}>
              <line
                className="caw-trend__grid"
                x1={MARGIN.left}
                x2={width - MARGIN.right}
                y1={y}
                y2={y}
              />
              <text
                className="caw-trend__axis"
                x={MARGIN.left - 8}
                y={y + 3}
                textAnchor="end"
              >
                {Math.round(value)}
              </text>
              {hasDuration ? (
                <text
                  className="caw-trend__axis"
                  x={width - MARGIN.right + 8}
                  y={y + 3}
                >
                  {Math.round((minuteMax / 4) * tick)}
                </text>
              ) : null}
            </g>
          );
        })}
        {points.map((point, index) => {
          const center = x(index);
          return (
            <g key={point.key}>
              <rect
                x={center - slot / 2}
                y={MARGIN.top}
                width={slot}
                height={plotHeight}
                fill="transparent"
              >
                <title>
                  {point.label}：审查 {point.started} 次，完成 {point.completed}{" "}
                  次，平均时长 {formatDuration(point.averageMs)}
                </title>
              </rect>
              <rect
                className="caw-trend__bar-started"
                x={center - barWidth - 1}
                y={yCount(point.started)}
                width={barWidth}
                height={Math.max(0, yCount(0) - yCount(point.started))}
                rx="2"
              />
              <rect
                className="caw-trend__bar-completed"
                x={center + 1}
                y={yCount(point.completed)}
                width={barWidth}
                height={Math.max(0, yCount(0) - yCount(point.completed))}
                rx="2"
              />
              {index % (compact ? 3 : 2) === 0 ? (
                <text
                  className="caw-trend__axis"
                  x={center}
                  y={height - 10}
                  textAnchor="middle"
                >
                  {point.label}
                </text>
              ) : null}
            </g>
          );
        })}
        {hasDuration ? (
          <>
            <path className="caw-trend__line" d={linePath} />
            {points.map((point, index) =>
              point.averageMs === null ? null : (
                <circle
                  key={`point-${point.key}`}
                  className="caw-trend__point"
                  cx={x(index)}
                  cy={yMinutes(minutes[index]!)}
                  r="3"
                >
                  <title>
                    {point.label} 平均时长 {formatDuration(point.averageMs)}
                  </title>
                </circle>
              ),
            )}
          </>
        ) : null}
      </svg>
      <table className="sr-only">
        <caption>近 14 天审查趋势数据</caption>
        <thead>
          <tr>
            <th scope="col">日期</th>
            <th scope="col">审查数量</th>
            <th scope="col">完成数量</th>
            <th scope="col">平均时长</th>
          </tr>
        </thead>
        <tbody>
          {points.map((point) => (
            <tr key={point.key}>
              <th scope="row">{point.label}</th>
              <td>{point.started}</td>
              <td>{point.completed}</td>
              <td>{formatDuration(point.averageMs)}</td>
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
