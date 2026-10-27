export function CircuitMark({ size = 28 }: { size?: number }) {
  return (
    <svg
      viewBox="0 0 28 28"
      width={size}
      height={size}
      fill="none"
      aria-hidden="true"
    >
      <path
        d="M3 8h7v6h8v6h7"
        stroke="currentColor"
        strokeWidth="1.4"
        strokeLinecap="square"
      />
      <rect x="2" y="7" width="3" height="3" fill="currentColor" />
      <rect x="10" y="13" width="3" height="3" fill="currentColor" />
      <rect x="18" y="19" width="3" height="3" fill="currentColor" />
      <rect
        x="25"
        y="19"
        width="3"
        height="3"
        fill="none"
        stroke="currentColor"
        strokeWidth="1.4"
      />
    </svg>
  );
}
