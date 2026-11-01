import { Alert, Button, Select, Tag } from "antd";
import { useState } from "react";
import {
  checkContractReferences,
  type ContractReferenceCheck,
  type DraftContent,
} from "../api/contracts";
import { errorMessage } from "../api/http";
import { useCases } from "../api/queries";

/**
 * Read-only view of the contract reference chain.
 *
 * The panel never writes. It calls the strict resolver, shows the frozen or
 * current subject/material pins, and links previews to the existing
 * document-service original endpoint. That endpoint re-checks the source case
 * on every request, so the link is not an authorization bypass.
 */
export function ContractReferencesPanel({
  contractID,
  draft,
}: {
  contractID: string | null;
  draft: DraftContent;
}) {
  const cases = useCases();
  const [caseID, setCaseID] = useState<string | null>(null);
  const [check, setCheck] = useState<ContractReferenceCheck | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);

  const hasReferences =
    (draft.contract.related_parties?.length ?? 0) > 0 ||
    (draft.materials?.length ?? 0) > 0 ||
    draft.contract.procurement_result_package !== undefined;

  async function run() {
    if (contractID === null) return;
    setBusy(true);
    setError(null);
    try {
      setCheck(await checkContractReferences(contractID, caseID ?? undefined));
    } catch (caught) {
      setCheck(null);
      setError(errorMessage(caught));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="contracts-page-references">
      <p className="meta">
        核对会按来源案件重新解析主体绑定、材料版本和采购结果来源；草稿保存保持宽松，冻结时才固定来源快照。
        预览/下载复用既有原件端点，每次访问都会重新检查案件读权限和原件摘要。
      </p>
      {contractID === null ? (
        <p className="meta">保存草稿后可核对引用。</p>
      ) : (
        <>
          <div className="contracts-page-fields">
            <Select
              showSearch
              allowClear
              optionFilterProp="label"
              value={caseID ?? undefined}
              placeholder="来源案件（草稿必选；已导入合同可留空）"
              loading={cases.isLoading}
              options={(cases.data ?? []).map((item) => ({ value: item.id, label: item.title }))}
              onChange={(value) => setCaseID(value ?? null)}
            />
            <Button loading={busy} onClick={() => void run()}>
              核对引用
            </Button>
          </div>
          {!hasReferences ? (
            <p className="meta">当前快照没有主体、材料或采购结果引用；核对仍会验证来源案件读权限。</p>
          ) : null}
          {error !== null ? <Alert type="error" showIcon message={error} /> : null}
          {check !== null ? (
            <div className="contracts-page-reference-result">
              <p className="meta">
                来源案件 {check.source_case_id} ·{" "}
                {check.source === "revision"
                  ? `冻结版本 ${check.contract_revision_id ?? ""}`
                  : "当前草稿"}{" "}
                · 核对时间 {check.checked_at}
              </p>
              {check.subjects.length > 0 ? (
                <>
                  <strong>主体引用</strong>
                  <ul className="contracts-page-materials">
                    {check.subjects.map((subject) => (
                      <li key={`${subject.binding_id}-${subject.binding_version}`}>
                        <Tag>{subject.role}</Tag>
                        <span>{subject.name_snapshot}</span>
                        <span className="meta">
                          绑定 {subject.binding_id} v{subject.binding_version} · 主体 v
                          {subject.subject_version} · {subject.verification_state}
                          {subject.frozen ? " · 冻结" : ""}
                        </span>
                      </li>
                    ))}
                  </ul>
                </>
              ) : null}
              {check.materials.length > 0 ? (
                <>
                  <strong>材料引用</strong>
                  <ul className="contracts-page-materials">
                    {check.materials.map((material) => (
                      <li key={`${material.material_id}-${material.material_version_id}`}>
                        <Tag>{material.material_role}</Tag>
                        <span>{material.filename}</span>
                        <span className="contracts-page-hash">{material.sha256}</span>
                        <a href={material.original_path} target="_blank" rel="noreferrer">
                          预览/下载
                        </a>
                        {material.frozen ? <span className="meta">冻结</span> : null}
                      </li>
                    ))}
                  </ul>
                </>
              ) : null}
              {check.procurement_result !== undefined ? (
                <>
                  <strong>采购结果来源</strong>
                  <p className="meta">
                    {check.procurement_result.system} / {check.procurement_result.result_id} /{" "}
                    {check.procurement_result.package_id} · 材料{" "}
                    {check.procurement_result.material_id} v
                    {check.procurement_result.material_version} ·{" "}
                    {check.procurement_result.material_role}
                  </p>
                </>
              ) : null}
              {check.warnings.length > 0 ? (
                <p className="meta">提示：{check.warnings.join("、")}</p>
              ) : null}
            </div>
          ) : null}
        </>
      )}
    </div>
  );
}
