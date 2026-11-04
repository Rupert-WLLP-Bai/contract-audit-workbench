import type { ReactNode } from "react";

// Full labels remain searchable; wrapped options expose every character of the
// secondary machine identity, even when the closed control is narrow.
export const caseSelectPresentation = {
  virtual: false,
  styles: { popup: { listItem: { whiteSpace: "normal" as const } } },
  optionRender: (option: { label?: ReactNode }) =>
    <div style={{ whiteSpace: "normal", overflowWrap: "anywhere" }}>{option.label}</div>,
  labelRender: ({ label }: { label: ReactNode }) =>
    typeof label === "string" ? label.split(" · ").slice(0, 2).join(" · ") : label,
};
