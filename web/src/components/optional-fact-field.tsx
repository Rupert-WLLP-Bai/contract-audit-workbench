// File metadata (manifest mirror):
// Purpose: Renders OptionalFactField, the optional fact field workbench UI, from the selected case/workspace projection and the module’s query/state inputs; it displays server-derived state and does not establish business authorization or evidence validity.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-30
// Updated: 2026-10-30
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { Input } from "antd";

export function OptionalFactField({
  id,
  label,
  value,
  disabled,
  onChange,
}: {
  id: string;
  label: string;
  value: string;
  disabled: boolean;
  onChange: (value: string) => void;
}) {
  return (
    <label className="field" htmlFor={id}>
      {label}
      <Input
        disabled={disabled}
        id={id}
        value={value}
        onChange={(event) => onChange(event.target.value)}
      />
    </label>
  );
}
