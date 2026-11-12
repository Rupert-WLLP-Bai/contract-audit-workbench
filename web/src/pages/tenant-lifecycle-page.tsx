// File metadata (manifest mirror):
// Purpose: 实现 `ProvisionKind`, `ProvisionReconciliation`, `ProvisionStepName`, `TenantProvisionInput` 等界面组件或状态处理，呈现服务端返回的业务数据并提交受控用户操作。
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-02
// Updated: 2026-11-08
// Revision: r2
// Copyright: See LICENSE
// End file metadata.

import { useEffect, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import {
  Alert,
  Button,
  Card,
  Descriptions,
  Form,
  Input,
  Modal,
  Select,
  Space,
  Table,
  Tag,
  type TableProps,
} from "antd";
import {
  ApiError,
  captureCommandContext,
  errorMessage,
  invalidateSessionIdentity,
  isForbidden,
  isUnauthenticated,
} from "../api/http";
import {
  clearPendingMutationKey,
  idempotencyActorScope,
  pendingMutationKey,
} from "../api/idempotency";
import type { Actor } from "../api/types";
import {
  createTenantProvisionOperation,
  recordTenantProvisionStep,
  useTenantProvisionOperation,
  useTenantProvisionOperations,
  type ProvisionKind,
  type ProvisionReconciliation,
  type ProvisionStepName,
  type TenantProvisionInput,
  type TenantProvisionOperation,
  type TenantProvisionStepInput,
} from "../api/tenant-provisioning";
import {
  clearIdentityQueries,
  queryKeys,
  revalidateSessionContext,
  useSession,
} from "../api/queries";
import { PageStatus } from "../components/page-status";
import "./tenant-lifecycle-page.css";

const KIND_LABEL: Record<ProvisionKind, string> = {
  provision: "开通",
  deactivate: "停用",
  reactivate: "恢复",
};
const STATUS_LABEL: Record<TenantProvisionOperation["status"], string> = {
  pending: "待处理",
  in_progress: "进行中",
  reconciliation_required: "待核对外部结果",
  provision_failed: "操作失败",
  completed: "已完成",
};
const STATUS_COLOR: Record<TenantProvisionOperation["status"], string> = {
  pending: "default",
  in_progress: "processing",
  reconciliation_required: "warning",
  provision_failed: "error",
  completed: "success",
};
const STEP_LABEL: Record<ProvisionStepName, string> = {
  identity: "身份绑定",
  tenant_config: "租户配置",
  runtime_ready: "运行就绪",
  drain: "停止新任务并收尾",
  usage_reconcile: "核对在途用量",
  completed: "完成",
};
const STEP_STATE_LABEL: Record<TenantProvisionOperation["steps"][number]["state"], string> = {
  pending: "待处理",
  complete: "已记录回执",
  external_unknown: "外部结果未知",
  failed: "失败",
  reconciled: "已核对",
};
const RECONCILIATION_OPTIONS: { value: ProvisionReconciliation; label: string }[] = [
  { value: "confirmed_complete", label: "已确认外部操作完成" },
  { value: "confirmed_not_applied", label: "已确认外部操作未执行" },
  { value: "requires_operator", label: "结果仍需平台人员处理" },
];

function isContextStale(error: unknown): boolean {
  return (
    (error instanceof ApiError && error.code === "context_stale") ||
    (error instanceof DOMException && error.name === "AbortError")
  );
}

function hasApiCode(error: unknown, code: string): boolean {
  return error instanceof ApiError && error.code === code;
}

async function refreshAdminContext(queryClient: ReturnType<typeof useQueryClient>): Promise<void> {
  try {
    await revalidateSessionContext(queryClient, "admin");
  } catch {
    invalidateSessionIdentity();
    clearIdentityQueries(queryClient);
    await queryClient.resetQueries({ queryKey: queryKeys.sessionFor("admin") });
  }
}

type OperationForm = Omit<TenantProvisionInput, "idempotency_key">;
function OperationCreator({
  actor,
  onCreated,
}: {
  actor: Actor;
  onCreated: (operation: TenantProvisionOperation) => void;
}) {
  const [form] = Form.useForm<OperationForm>();
  const [kind, setKind] = useState<ProvisionKind>("provision");
  const [notice, setNotice] = useState<string | null>(null);
  const [preparing, setPreparing] = useState(false);
  const queryClient = useQueryClient();
  const create = useMutation({
    mutationFn: ({
      input,
      context,
    }: {
      input: TenantProvisionInput;
      context: ReturnType<typeof captureCommandContext>;
      storageKey: string;
    }) => createTenantProvisionOperation(input, context),
    onSuccess: async (result, variables) => {
      onCreated(result.operation);
      setNotice(
        `已登记${KIND_LABEL[result.operation.kind]}操作 ${result.operation.operation_id}。`,
      );
      clearPendingMutationKey(variables.storageKey);
      await queryClient.invalidateQueries({
        queryKey: ["tenant-provision-operations", "platform"],
      });
    },
    onError: async (error, variables) => {
      setNotice(errorMessage(error));
      if (isContextStale(error)) {
        clearPendingMutationKey(variables.storageKey);
        form.resetFields();
        await refreshAdminContext(queryClient);
      }
    },
  });
  const onFinish = (values: OperationForm) => {
    setNotice(null);
    Modal.confirm({
      title: `确认登记${KIND_LABEL[values.kind]}操作？`,
      content:
        values.kind === "deactivate"
          ? `将为租户 ${values.tenant_id} 登记停用并进入在途用量核对。操作不会删除历史记录。`
          : `将为租户 ${values.tenant_id} 登记${KIND_LABEL[values.kind]}操作。管理员主体与租户配置由服务端重新验证；不会创建密码或身份凭据。`,
      okText: "确认登记",
      cancelText: "取消",
      onOk: async () => {
        setPreparing(true);
        try {
          const commandContext = captureCommandContext(actor);
          const payload: OperationForm =
            values.kind === "deactivate"
              ? {
                  ...values,
                  tenant_name: "",
                  jurisdiction_id: "",
                  admin_principal_id: "",
                  admin_org_unit_id: "",
                  admin_org_unit_name: "",
                  tenant_config_version: "",
                }
              : values;
          const pending = await pendingMutationKey(
            "tenant-provision-operation",
            idempotencyActorScope(actor),
            payload.tenant_id,
            payload,
            commandContext,
          );
          create.mutate({
            input: { ...payload, idempotency_key: pending.key },
            context: commandContext,
            storageKey: pending.storageKey,
          });
        } catch (error) {
          setNotice(errorMessage(error));
          if (isContextStale(error)) {
            form.resetFields();
            await refreshAdminContext(queryClient);
          }
          throw error;
        } finally {
          setPreparing(false);
        }
      },
    });
  };
  const requiresSetup = kind !== "deactivate";
  return (
    <Card
      className="lifecycle-create-card"
      title="登记生命周期操作"
      extra={<Tag color="processing">受控平台操作</Tag>}
    >
      <p>
        操作需要平台 actor allowlist 与数据库 principal allowlist
        双重授权。开通和恢复必须引用已有管理员主体与租户配置；停用立即停止新准入，再进入在途用量核对。此处不会创建密码或身份凭据。
      </p>
      <Form
        form={form}
        layout="vertical"
        initialValues={{
          kind: "provision",
          tenant_name: "",
          jurisdiction_id: "",
          admin_principal_id: "",
          admin_org_unit_id: "",
          admin_org_unit_name: "",
          tenant_config_version: "",
        }}
        onFinish={onFinish}
      >
        <div className="lifecycle-form-grid">
          <Form.Item name="kind" label="操作类型" rules={[{ required: true }]}>
            <Select
              options={Object.entries(KIND_LABEL).map(([value, label]) => ({ value, label }))}
              onChange={(value: ProvisionKind) => {
                setKind(value);
                if (value === "deactivate")
                  form.setFieldsValue({
                    tenant_name: "",
                    jurisdiction_id: "",
                    admin_principal_id: "",
                    admin_org_unit_id: "",
                    admin_org_unit_name: "",
                    tenant_config_version: "",
                  });
              }}
            />
          </Form.Item>
          <Form.Item
            name="tenant_id"
            label="租户标识"
            rules={[{ required: true, whitespace: true }]}
          >
            <Input autoComplete="off" />
          </Form.Item>
          <Form.Item
            hidden={!requiresSetup}
            name="tenant_name"
            label="租户名称"
            rules={requiresSetup ? [{ required: true, whitespace: true }] : []}
          >
            <Input autoComplete="off" />
          </Form.Item>
          <Form.Item
            hidden={!requiresSetup}
            name="jurisdiction_id"
            label="辖区标识"
            rules={requiresSetup ? [{ required: true, whitespace: true }] : []}
          >
            <Input autoComplete="off" />
          </Form.Item>
          <Form.Item
            hidden={!requiresSetup}
            name="admin_principal_id"
            label="已有管理员主体标识"
            rules={requiresSetup ? [{ required: true, whitespace: true }] : []}
          >
            <Input autoComplete="off" />
          </Form.Item>
          <Form.Item
            hidden={!requiresSetup}
            name="admin_org_unit_id"
            label="管理员组织单元标识"
            rules={requiresSetup ? [{ required: true, whitespace: true }] : []}
          >
            <Input autoComplete="off" />
          </Form.Item>
          <Form.Item
            hidden={!requiresSetup}
            name="admin_org_unit_name"
            label="管理员组织单元名称"
            rules={requiresSetup ? [{ required: true, whitespace: true }] : []}
          >
            <Input autoComplete="off" />
          </Form.Item>
          <Form.Item
            hidden={!requiresSetup}
            name="tenant_config_version"
            label="已发布租户配置版本"
            rules={requiresSetup ? [{ required: true, whitespace: true }] : []}
          >
            <Input autoComplete="off" />
          </Form.Item>
        </div>
        <Space wrap>
          <Button type="primary" htmlType="submit" loading={create.isPending || preparing}>
            登记{KIND_LABEL[kind]}操作
          </Button>
          {notice ? <span role="status">{notice}</span> : null}
        </Space>
        {create.isError && isForbidden(create.error) ? (
          <Alert
            className="lifecycle-alert"
            type="error"
            message="当前身份没有平台租户生命周期操作权限。"
          />
        ) : null}
      </Form>
    </Card>
  );
}
function OperationDetail({
  operationId,
  enabled,
  actor,
}: {
  operationId: string | null;
  enabled: boolean;
  actor: NonNullable<ReturnType<typeof useSession>["data"]>;
}) {
  const queryClient = useQueryClient();
  const [reconciliation, setReconciliation] = useState<ProvisionReconciliation | "">("");
  const [receipt, setReceipt] = useState("");
  const [notice, setNotice] = useState<string | null>(null);
  const detail = useTenantProvisionOperation(operationId, enabled);
  const mutation = useMutation({
    mutationFn: ({
      operation,
      input,
      context,
    }: {
      operation: TenantProvisionOperation;
      input: TenantProvisionStepInput;
      context: ReturnType<typeof captureCommandContext>;
    }) => recordTenantProvisionStep(operation.operation_id, input, context),
    onSuccess: async (result) => {
      setNotice("步骤回执已提交，页面将刷新。");
      setReceipt("");
      await queryClient.setQueryData(
        ["tenant-provision-operation", result.operation.operation_id],
        result.operation,
      );
      await queryClient.invalidateQueries({
        queryKey: ["tenant-provision-operations", "platform"],
      });
    },
    onError: async (error) => {
      setNotice(errorMessage(error));
      const staleContext = isContextStale(error);
      if (staleContext || hasApiCode(error, "tenant_operation_revision_conflict")) {
        setReceipt("");
        setReconciliation("");
        if (staleContext) await refreshAdminContext(queryClient);
        await Promise.all([
          detail.refetch(),
          queryClient.invalidateQueries({ queryKey: ["tenant-provision-operations", "platform"] }),
        ]);
      }
    },
  });
  const operation = detail.data;
  useEffect(() => {
    setReceipt("");
    setReconciliation("");
    setNotice(null);
  }, [operationId, operation?.revision]);
  const currentStep = operation?.steps.find((step) => step.name === operation.current_step);
  const submit = (
    state: TenantProvisionStepInput["state"],
    reconcile: TenantProvisionStepInput["reconciliation"] = "",
  ) => {
    if (!operation) return;
    setNotice(null);
    const normalizedReceipt = receipt.trim();
    if ((state === "complete" || state === "reconciled") && normalizedReceipt === "") {
      setNotice("完成步骤或核对外部结果时必须提供有效的回执 SHA-256。");
      return;
    }
    if (normalizedReceipt !== "" && !/^[0-9a-f]{64}$/.test(normalizedReceipt)) {
      setNotice("回执 SHA-256 必须是 64 位小写十六进制字符串。");
      return;
    }
    try {
      const commandContext = captureCommandContext(actor);
      mutation.mutate({
        operation,
        input: {
          expected_revision: operation.revision,
          state,
          receipt_sha256: normalizedReceipt || null,
          reconciliation: reconcile,
        },
        context: commandContext,
      });
    } catch (error) {
      setReceipt("");
      setReconciliation("");
      setNotice(errorMessage(error));
      if (isContextStale(error)) void refreshAdminContext(queryClient);
    }
  };
  if (!operationId)
    return (
      <Card className="lifecycle-detail-empty">
        <p>选择一条操作查看步骤回执和当前 revision。</p>
      </Card>
    );
  if (detail.isLoading) return <Card loading title="操作详情" />;
  if (detail.isError)
    return (
      <Card title="操作详情">
        <Alert
          type={isForbidden(detail.error) ? "error" : "warning"}
          message={
            isForbidden(detail.error) ? "当前身份无权查看该操作。" : errorMessage(detail.error)
          }
        />
        <Button onClick={() => void detail.refetch()}>重新读取</Button>
      </Card>
    );
  if (!operation) return null;
  const stepColumns: TableProps<TenantProvisionOperation["steps"][number]>["columns"] = [
    { title: "步骤", dataIndex: "name", render: (value: ProvisionStepName) => STEP_LABEL[value] },
    {
      title: "回执状态",
      dataIndex: "state",
      render: (value: TenantProvisionOperation["steps"][number]["state"]) =>
        value === "external_unknown" ? (
          <Tag color="warning">{STEP_STATE_LABEL[value]}</Tag>
        ) : (
          STEP_STATE_LABEL[value]
        ),
    },
    {
      title: "回执 SHA-256",
      dataIndex: "receipt_sha256",
      render: (value: string | null) => (value ? <code>{value}</code> : "未提供"),
    },
    {
      title: "更新时间",
      dataIndex: "updated_at",
      render: (value: string | null) =>
        value ? new Date(value).toLocaleString("zh-CN", { timeZone: "UTC" }) + " UTC" : "未记录",
    },
  ];
  const recordConfirmed = (
    state: TenantProvisionStepInput["state"],
    reconciliationValue: TenantProvisionStepInput["reconciliation"] = "",
  ) => {
    Modal.confirm({
      title:
        state === "external_unknown"
          ? "确认记录外部结果未知？"
          : state === "reconciled"
            ? "确认已核对外部结果？"
            : "确认提交步骤回执？",
      content:
        state === "external_unknown"
          ? "服务会阻止推进后续步骤。请勿将未知状态当作失败后直接重试。"
          : state === "reconciled"
            ? "请确认这是从外部权威处核对的结果。页面只写回核对回执，不会重新执行外部操作。"
            : "此操作只记录人工确认的步骤回执。",
      okText: "确认记录",
      cancelText: "取消",
      onOk: () => submit(state, reconciliationValue),
    });
  };
  return (
    <Card
      className="lifecycle-detail"
      title="操作详情"
      extra={<Button onClick={() => void detail.refetch()}>刷新状态</Button>}
    >
      <Descriptions size="small" column={{ xs: 1, md: 2 }}>
        <Descriptions.Item label="操作 ID">
          <code>{operation.operation_id}</code>
        </Descriptions.Item>
        <Descriptions.Item label="租户">
          <span>{operation.tenant_id}</span>
        </Descriptions.Item>
        <Descriptions.Item label="操作类型">{KIND_LABEL[operation.kind]}</Descriptions.Item>
        <Descriptions.Item label="状态">
          <Tag color={STATUS_COLOR[operation.status]}>{STATUS_LABEL[operation.status]}</Tag>
        </Descriptions.Item>
        <Descriptions.Item label="当前步骤">{STEP_LABEL[operation.current_step]}</Descriptions.Item>
        <Descriptions.Item label="revision">{operation.revision}</Descriptions.Item>
        <Descriptions.Item label="payload SHA-256">
          <code>{operation.payload_sha256}</code>
        </Descriptions.Item>
        <Descriptions.Item label="创建时间">
          {new Date(operation.created_at).toLocaleString("zh-CN", { timeZone: "UTC" })} UTC
        </Descriptions.Item>
      </Descriptions>
      {operation.reconciliation_required ? (
        <Alert
          className="lifecycle-alert"
          type="warning"
          showIcon
          message="外部操作结果待核对"
          description="当前操作锁定在此处。先从外部权威核对结果并留下回执；不要直接重放可能已经执行的动作。"
        />
      ) : null}
      <Table
        rowKey="name"
        size="small"
        pagination={false}
        columns={stepColumns}
        dataSource={operation.steps}
      />
      {operation.status === "provision_failed" ? (
        <Alert
          className="lifecycle-alert"
          type="error"
          showIcon
          message="当前操作已终止"
          description="失败操作不能重放。修复前置问题并重新核对租户状态后，需登记新的显式操作；当前记录和回执会保留。"
        />
      ) : operation.status !== "completed" ? (
        <section className="lifecycle-step-editor" aria-label="记录当前步骤">
          <h3>记录当前步骤：{STEP_LABEL[operation.current_step]}</h3>
          {currentStep?.state === "external_unknown" || operation.reconciliation_required ? (
            <>
              <label>
                人工核对结论
                <Select
                  value={reconciliation || undefined}
                  placeholder="请选择核对结论"
                  onChange={setReconciliation}
                  options={RECONCILIATION_OPTIONS}
                />
              </label>
              <label>
                外部核对回执 SHA-256（必填）
                <Input
                  value={receipt}
                  onChange={(event) => setReceipt(event.target.value)}
                  autoComplete="off"
                />
              </label>
              <Button
                type="primary"
                disabled={mutation.isPending || reconciliation === ""}
                onClick={() => recordConfirmed("reconciled", reconciliation)}
              >
                提交核对结果
              </Button>
            </>
          ) : (
            <>
              <label>
                步骤回执 SHA-256（完成/核对时必填）
                <Input
                  value={receipt}
                  onChange={(event) => setReceipt(event.target.value)}
                  autoComplete="off"
                />
              </label>
              <Space wrap>
                <Button
                  type="primary"
                  disabled={mutation.isPending}
                  onClick={() => recordConfirmed("complete")}
                >
                  确认步骤已完成
                </Button>
                <Button
                  disabled={mutation.isPending}
                  onClick={() => recordConfirmed("external_unknown")}
                >
                  记录外部结果未知
                </Button>
                <Button
                  danger
                  disabled={mutation.isPending}
                  onClick={() => recordConfirmed("failed")}
                >
                  记录步骤失败
                </Button>
              </Space>
            </>
          )}
          {notice ? <p role="status">{notice}</p> : null}
          {mutation.isError && isForbidden(mutation.error) ? (
            <Alert type="error" message="当前身份不能提交此操作回执。" />
          ) : null}
        </section>
      ) : (
        <p className="meta">操作已完成。归档和删除不属于本页的恢复动作。</p>
      )}
    </Card>
  );
}

export function TenantLifecyclePage() {
  const session = useSession("admin");
  const [selectedId, setSelectedId] = useState<string | null>(null);
  const [cursorStack, setCursorStack] = useState<(string | null)[]>([null]);
  const cursor = cursorStack[cursorStack.length - 1] ?? null;
  const pageQuery = useTenantProvisionOperations(
    cursor,
    session.isSuccess && Boolean(session.data),
  );
  const page = pageQuery.data;
  const actorIdentity = session.data
    ? [
        session.data.id,
        session.data.tenant_id,
        session.data.org_id,
        session.data.principal_id,
        session.data.active_context.context_version,
      ].join("\u0000")
    : "";
  useEffect(() => {
    setSelectedId(null);
    setCursorStack([null]);
  }, [actorIdentity]);
  if (session.isLoading || session.isPending)
    return <PageStatus kind="loading" title="正在确认平台操作权限" />;
  if (session.isError || !session.data)
    return (
      <PageStatus
        kind={isUnauthenticated(session.error) ? "denied" : "error"}
        title="无法确认管理身份"
        detail={errorMessage(session.error)}
      />
    );
  if (pageQuery.isLoading) return <PageStatus kind="loading" title="正在读取租户生命周期操作" />;
  if (pageQuery.isError && isForbidden(pageQuery.error))
    return (
      <PageStatus
        kind="denied"
        title="没有平台租户生命周期权限"
        detail="平台授权由服务端根据当前 actor 与数据库 principal 双重判定。"
      />
    );
  if (pageQuery.isError)
    return (
      <PageStatus
        kind="error"
        title="租户生命周期操作不可用"
        detail={errorMessage(pageQuery.error)}
        action={<Button onClick={() => void pageQuery.refetch()}>重试</Button>}
      />
    );
  const columns: TableProps<TenantProvisionOperation>["columns"] = [
    { title: "操作", dataIndex: "kind", render: (value: ProvisionKind) => KIND_LABEL[value] },
    {
      title: "租户",
      dataIndex: "tenant_id",
      render: (value: string, operation) => (
        <span>
          {value}
          <br />
          <small>{operation.operation_id}</small>
        </span>
      ),
    },
    {
      title: "当前步骤",
      dataIndex: "current_step",
      render: (value: ProvisionStepName) => STEP_LABEL[value],
    },
    {
      title: "状态",
      dataIndex: "status",
      render: (value: TenantProvisionOperation["status"]) => (
        <Tag color={STATUS_COLOR[value]}>{STATUS_LABEL[value]}</Tag>
      ),
    },
    {
      title: "更新",
      dataIndex: "updated_at",
      render: (value: string) =>
        new Date(value).toLocaleString("zh-CN", { timeZone: "UTC" }) + " UTC",
    },
    {
      title: "",
      key: "action",
      render: (_: unknown, operation) => (
        <Button type="link" onClick={() => setSelectedId(operation.operation_id)}>
          查看回执
        </Button>
      ),
    },
  ];
  return (
    <section className="page tenant-lifecycle-page">
      <header className="page-head">
        <div>
          <h1>租户生命周期管理</h1>
          <p>
            平台控制操作与租户自助运维分别授权。页面记录幂等操作、步骤回执和外部结果核对；未知结果不得盲目重试。
          </p>
        </div>
      </header>
      <OperationCreator
        actor={session.data}
        onCreated={(operation) => setSelectedId(operation.operation_id)}
      />
      <Card
        className="lifecycle-operations-card"
        title="持久化操作记录"
        extra={<Button onClick={() => void pageQuery.refetch()}>刷新</Button>}
      >
        <Table<TenantProvisionOperation>
          rowKey="operation_id"
          size="small"
          pagination={false}
          columns={columns}
          dataSource={page?.operations ?? []}
        />
        <Space className="lifecycle-pagination">
          <Button
            disabled={cursorStack.length < 2}
            onClick={() => setCursorStack((stack) => stack.slice(0, -1))}
          >
            上一页
          </Button>
          <Button
            disabled={!page?.next_cursor}
            onClick={() => setCursorStack((stack) => [...stack, page?.next_cursor ?? null])}
          >
            下一页
          </Button>
        </Space>
      </Card>
      <OperationDetail operationId={selectedId} enabled={session.isSuccess} actor={session.data} />
    </section>
  );
}
