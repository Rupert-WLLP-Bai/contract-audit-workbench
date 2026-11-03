// File metadata (manifest mirror):
// Purpose: Loads one authorized case and its review readiness on demand for the workbench, hiding stale or mismatched context before presenting the next step.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-03
// Updated: 2026-11-03
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { useQuery } from "@tanstack/react-query";
import { getReviewReadiness } from "../api/review-readiness";
import { useCaseDetail, useCaseReviewHistory, useSession } from "../api/queries";
import { errorMessage, isForbidden } from "../api/http";
import { CaseNextStep } from "./case-next-step";
import { PageStatus } from "./page-status";

export function WorkbenchCaseNextStep({ caseId }: { caseId: string }) {
  const detail = useCaseDetail(caseId);
  const session = useSession();
  const actorScope = [
    session.data?.id ?? "",
    session.data?.tenant_id ?? "",
    session.data?.org_id ?? "",
    session.data?.audience ?? "",
    session.data?.role ?? "",
    session.data?.active_context?.context_version ?? "",
  ].join(":");
  const history = useCaseReviewHistory(
    caseId,
    detail.data?.id === caseId ? detail.data.latest_run?.id ?? null : null,
  );
  const readinessDependencies = [
    detail.data?.updated_at,
    detail.data?.material_selection,
    detail.data?.documents.map((document) => [document.id, document.parse_status]),
  ] as const;
  const readiness = useQuery({
    queryKey: ["cases", caseId, "review-readiness", actorScope, "", ...readinessDependencies],
    queryFn: ({ signal }) => getReviewReadiness(caseId, "", signal),
    enabled: detail.isSuccess && detail.data?.id === caseId && session.isSuccess,
    retry: false,
  });
  const readinessLoading = detail.isFetching || session.isFetching || readiness.isFetching;
  const readinessError = readiness.isError ? readiness.error : null;
  const currentReadiness = !readinessLoading && detail.data?.id === caseId && !detail.isError && !session.isError && !readiness.isError
    ? readiness.data
    : undefined;

  if (detail.isLoading || detail.isFetching || session.isLoading || session.isFetching)
    return <PageStatus kind="loading" title="正在读取案件下一步" />;
  if (detail.isError || !detail.data) {
    const denied = isForbidden(detail.error);
    return <PageStatus kind={denied ? "denied" : "error"} title={denied ? "已无权查看此案件" : "案件下一步不可用"} detail={errorMessage(detail.error)} />;
  }
  if (detail.data.id !== caseId)
    return <PageStatus kind="error" title="案件身份不匹配" detail="案件服务返回的身份与当前所选案件不一致，已停止读取准备状态。" />;
  if (session.isError)
    return <PageStatus kind={"error"} title="身份状态不可用" detail={errorMessage(session.error)} action={<button type="button" onClick={() => void session.refetch()}>重新读取身份</button>} />;

  return (
    <CaseNextStep
      caseDetail={detail.data}
      actorRole={session.data?.role ?? ""}
      sessionReady={session.isSuccess}
      sessionLoading={session.isLoading}
      sessionError={null}
      readiness={currentReadiness}
      readinessLoading={readinessLoading}
      readinessError={readinessError}
      selectedScope="full_review"
      businessAtInput=""
      validBusinessAt={false}
      historyLoading={history.isFetching}
      historyError={history.isError ? history.error : null}
      startPending={false}
      startError={null}
      onRetrySession={() => void session.refetch()}
      onRefreshReadiness={() => void readiness.refetch()}
      onRefreshHistory={() => void history.refetch()}
      onStartReview={() => undefined}
      suppressWriteAction
    />
  );
}
