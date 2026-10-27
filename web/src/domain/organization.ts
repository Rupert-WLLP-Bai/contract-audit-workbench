const ORGANIZATION_NAMES: Record<string, string> = {
  "demo-org": "两江新区分公司",
};

export function organizationName(orgId: string | null | undefined): string {
  if (!orgId) return "正在确认";
  return ORGANIZATION_NAMES[orgId] ?? orgId;
}
