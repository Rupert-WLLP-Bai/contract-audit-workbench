import { isInvalidTime } from "../domain/time";
import { TechnicalDetail } from "./technical-detail";

// Keep malformed server timestamps available verbatim without presenting them as dates.
export function InvalidTimeDetail({ value, label }: { value: string | null | undefined; label: string }) {
  return isInvalidTime(value) ? <TechnicalDetail label={`${label}原始值`} value={value} /> : null;
}
