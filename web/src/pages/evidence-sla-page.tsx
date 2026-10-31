import { Button } from "antd";
import { errorMessage } from "../api/http";
import { useCases } from "../api/queries";
import { EvidenceSLADashboard } from "../components/evidence-sla-dashboard";
import { PageStatus } from "../components/page-status";

export function EvidenceSLAPage() {
  const cases = useCases();
  if (cases.isLoading)
    return <PageStatus kind="loading" title="正在读取授权案件" />;
  if (cases.isError)
    return (
      <PageStatus
        kind="error"
        title="补充材料时效看板没有加载"
        detail={errorMessage(cases.error)}
        action={
          <Button type="primary" onClick={() => void cases.refetch()}>
            重试
          </Button>
        }
      />
    );
  return (
    <section className="page evidence-sla-page">
      <header className="page-head">
        <div>
          <h1>补充材料时效</h1>
          <p>
            只读取当前账号可访问的案件，并逐案读取补充材料请求及其回执历史。
          </p>
        </div>
      </header>
      <EvidenceSLADashboard cases={cases.data ?? []} />
    </section>
  );
}
