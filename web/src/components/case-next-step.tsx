import { Button } from "antd";
import type { CaseDetail, Remediation } from "../api/types";
import type { ReviewReadiness, ReviewScope } from "../api/review-readiness";
import { errorMessage } from "../api/http";
import { actorRoleLabel, runLabel, type StatusTone } from "../domain/review";
import { StatusText } from "./status-text";
import "./case-next-step.css";

interface Props {
  caseDetail: CaseDetail;
  actorRole: string;
  sessionReady: boolean;
  sessionLoading: boolean;
  sessionError: unknown;
  readiness: ReviewReadiness | undefined;
  readinessLoading: boolean;
  readinessError: unknown;
  selectedScope: ReviewScope;
  businessAtInput: string;
  validBusinessAt: boolean;
  historyLoading: boolean;
  historyError: unknown;
  startPending: boolean;
  startError: unknown;
  onRetrySession: () => void;
  onRefreshReadiness: () => void;
  onRefreshHistory: () => void;
  onStartReview: () => void;
}

type ActionKind =
  | "link"
  | "start"
  | "refresh-session"
  | "refresh-readiness"
  | "refresh-history";

interface CaseNextStepPlan {
  stage: string;
  title: string;
  tone: StatusTone;
  reason: string;
  missingItems: string[];
  owner: string;
  action: { kind: ActionKind; label: string; href?: string; loading?: boolean; disabled?: boolean };
  announcement?: "alert" | "status";
}

const ACTIVE_RUN_STATES: Record<string, true> = {
  accepted: true,
  queued: true,
  started: true,
  running: true,
};
const KNOWN_RUN_STATES: Record<string, true> = {
  ...ACTIVE_RUN_STATES,
  awaiting_human: true,
  partial: true,
  completed: true,
  failed: true,
  cancelled: true,
  interrupted: true,
};
const INCOMPLETE_RUN_STATES: Record<string, true> = {
  failed: true,
  partial: true,
  cancelled: true,
  interrupted: true,
};
const KNOWN_REMEDIATION_STATES: Record<string, true> = {
  open: true,
  submitted: true,
  closed: true,
  reopened: true,
};
const CAN_START_REVIEW_ROLES: Record<string, true> = {
  admin: true,
  clerk: true,
  reviewer: true,
};
const CAN_CREATE_REPORT_ROLES: Record<string, true> = {
  admin: true,
  clerk: true,
};
const MATERIAL_BLOCKER_CODES: Record<string, true> = {
  required_materials_missing: true,
  material_selection_pending: true,
  primary_contract_required: true,
  primary_contract_ambiguous: true,
};
function roleFocus(role: string): string {
  if (role === "clerk") return "经办视图：优先补齐并确认材料、准备报告；审查结论由审查员人工决定。";
  if (role === "reviewer") return "审查视图：优先核对冻结原文、处理未裁决问题和补件复核；材料写入由经办负责。";
  if (role === "admin") return "管理员视图：可协助材料、审查与报告流程；人工决定仍由具备审查权限的人员提交。";
  if (role === "viewer") return "只读视图：可查看当前阶段与依据；材料、决定、补件和报告操作由有权角色处理。";
  return "当前用户角色未返回，按只读处理；请重新读取身份后再执行写入。";
}

function readinessAction(blockerCode: string, caseId: string): { href: string; label: string } {
  if (blockerCode === "business_at_required") return { href: "#review-business-time", label: "转到业务时间字段" };
  if (Object.hasOwn(MATERIAL_BLOCKER_CODES, blockerCode)) return { href: "#case-materials", label: "转到材料集合" };
  if (blockerCode === "governed_dependencies_incomplete") return { href: `/cases/${encodeURIComponent(caseId)}/legal-knowledge`, label: "打开法律依据与制度" };
  if (blockerCode === "case_not_writable") return { href: "#case-business-title", label: "查看业务状态" };
  return { href: "#review-readiness", label: "查看全部准备项" };
}

function newReviewReason(item: CaseDetail): string[] {
  const reasons: string[] = [];
  if (item.revision_pending) reasons.push("材料版本与最近一次审查不同");
  if (item.configuration_pending) reasons.push("审查配置与最近一次审查不同");
  if (item.subject_context_changed_after_latest_run === true) reasons.push("主体信息与最近一次审查不同");
  return reasons;
}

function remediationOwner(item: CaseDetail, remediation: Remediation): string {
  if (!remediation.assignee_id) return "未分配整改负责人";
  const member = item.members.find((entry) => entry.id === remediation.assignee_id);
  return member?.display_name
    ? `整改负责人：${member.display_name}`
    : "已指定整改负责人，成员资料未返回";
}

function planFor(props: Props): CaseNextStepPlan {
  const {
    caseDetail: item,
    actorRole,
    sessionReady,
    sessionLoading,
    sessionError,
    readiness,
    readinessLoading,
    readinessError,
    selectedScope,
    businessAtInput,
    validBusinessAt,
    historyLoading,
    historyError,
    startPending,
    startError,
  } = props;
  const run = item.latest_run;
  const reviewHref = run
    ? `/cases/${encodeURIComponent(item.id)}/reviews/${encodeURIComponent(run.id)}`
    : "#review-readiness";
  const canStart = Object.hasOwn(CAN_START_REVIEW_ROLES, actorRole);
  const canCreateReport = Object.hasOwn(CAN_CREATE_REPORT_ROLES, actorRole);
  const archived = item.archived_at !== null;
  const subjectContextUnknown = run !== null && item.subject_context_changed_after_latest_run === undefined;

  if (archived) {
    return {
      stage: "已归档",
      title: "案件处于只读状态",
      tone: "neutral",
      reason: "已归档案件不能继续上传材料、发起审查或写入整改；现有审查与报告仍可查看。",
      missingItems: [],
      owner: "只读查看",
      action: { kind: "link", href: "#report-archive-title", label: "查看已有报告与归档记录" },
    };
  }

  if (!sessionReady) {
    return {
      stage: sessionLoading ? "正在读取身份" : "身份状态不可用",
      title: "当前角色尚未确认",
      tone: sessionError ? "attention" : "progress",
      reason: sessionError
        ? `无法读取当前用户身份：${errorMessage(sessionError)}`
        : "身份和权限尚未返回；确认前不显示可写操作。",
      missingItems: ["当前用户身份与角色"],
      owner: "当前用户",
      action: { kind: "refresh-session", label: sessionLoading ? "正在读取身份" : "重新读取当前身份", loading: sessionLoading },
      announcement: sessionError ? "alert" : "status",
    };
  }

  if (run && Object.hasOwn(ACTIVE_RUN_STATES, run.state)) {
    return {
      stage: runLabel(run.state),
      title: "最近一次审查正在处理",
      tone: "progress",
      reason: "新上传的材料不会自动进入这条已受理审查；请在同一工作区查看执行进度与结果。",
      missingItems: [],
      owner: "审查员",
      action: { kind: "link", href: reviewHref, label: "打开最近审查" },
    };
  }

  if (run && !Object.hasOwn(KNOWN_RUN_STATES, run.state)) {
    return {
      stage: "状态未识别",
      title: "暂不能确认下一项写入操作",
      tone: "attention",
      reason: `服务端返回了未识别的运行状态“${run.state || "空"}”。为避免创建错误的后续记录，请先查看这次审查。`,
      missingItems: ["可识别的审查运行状态"],
      owner: "审查员或案件管理员",
      action: { kind: "link", href: reviewHref, label: "查看最近审查状态" },
    };
  }


  const refreshRequiredReview = run !== null && newReviewReason(item).length > 0;
  if (refreshRequiredReview || run === null) {
    if (subjectContextUnknown) {
      return {
        stage: "主体状态待确认",
        title: "服务端没有返回主体变化状态",
        tone: "attention",
        reason: "无法确认当前主体信息是否与最近一次冻结审查一致；不能据此发起新的审查。",
        missingItems: ["当前主体与最近一次审查之间的服务端比较结果"],
        owner: "案件管理员",
        action: { kind: "link", href: "#case-subjects", label: "查看案件主体信息" },
      };
    }
    if (item.material_selection?.status === "pending_confirmation") {
      return {
        stage: "材料集合待确认",
        title: "先确认后续审查使用的材料",
        tone: "attention",
        reason: "旧材料集合尚未确认；系统不会根据材料角色自动推断要审查哪些文档。",
        missingItems: ["确认本次审查材料集合"],
        owner: canStart ? "经办" : "有材料写入权限的经办或管理员",
        action: { kind: "link", href: "#case-materials", label: "确认材料集合" },
      };
    }

    if (!item.materials || !item.material_versions || !item.material_selection) {
      return {
        stage: "材料信息不完整",
        title: "无法确认当前审查材料",
        tone: "attention",
        reason: "服务端没有返回材料集合与固定版本信息，不能安全发起新审查。",
        missingItems: ["材料集合、材料版本或集合确认状态"],
        owner: "案件管理员",
        action: { kind: "link", href: "#case-materials", label: "查看材料信息" },
      };
    }

    if (startError) {
      return {
        stage: "审查请求未受理",
        title: "需要重新核对审查准备状态",
        tone: "attention",
        reason: `${errorMessage(startError)}。服务端会在发起时再次校验权限、材料版本和依赖。`,
        missingItems: ["当前审查准备清单的最新状态"],
        owner: "经办或审查员",
        action: { kind: "refresh-readiness", label: "刷新准备清单", loading: readinessLoading },
        announcement: "alert",
      };
    }

    if (businessAtInput !== "" && !validBusinessAt) {
      return {
        stage: "业务时间无效",
        title: "请修正审查业务时间",
        tone: "attention",
        reason: "已输入的北京时间无法转换为有效时点；当前不会提交该值。",
        missingItems: ["有效的北京时间（UTC+08:00）"],
        owner: "经办或审查员",
        action: { kind: "link", href: "#review-business-time", label: "修正业务时间" },
      };
    }

    if (item.subject_context_changed_after_latest_run === true) {
      const scope = readiness?.scopes.find((entry) => entry.id === selectedScope);
      if (scope?.can_start === true && !readinessLoading && !readinessError && !canStart) {
        return {
          stage: "主体信息已变更",
          title: "等待具备发起权限的人员复审",
          tone: "attention",
          reason: "主体信息已变化，新审查会冻结当前主体信息；旧审查、决定和报告会保留。",
          missingItems: ["由具备审查权限的经办、审查员或管理员发起复审"],
          owner: "经办、审查员或管理员",
          action: { kind: "link", href: "#review-readiness", label: "查看复审准备状态" },
        };
      }
    }

    if (readinessLoading) {
      return {
        stage: "正在核对准备状态",
        title: "等待服务端返回最新准备清单",
        tone: "progress",
        reason: "准备清单刷新期间不启用发起操作；服务端仍会在受理请求时重新校验。",
        missingItems: ["最新材料、规则与依赖核对结果"],
        owner: "经办或审查员",
        action: { kind: "refresh-readiness", label: "正在刷新准备清单", loading: true, disabled: true },
        announcement: "status",
      };
    }

    if (readinessError) {
      return {
        stage: "准备状态不可用",
        title: "无法确认是否可以开始审查",
        tone: "attention",
        reason: `准备清单读取失败：${errorMessage(readinessError)}。未知状态按不可开始处理。`,
        missingItems: ["服务端审查准备清单"],
        owner: "经办或审查员",
        action: { kind: "refresh-readiness", label: "重试读取准备清单" },
        announcement: "alert",
      };
    }

    if (!readiness) {
      return {
        stage: "准备状态未返回",
        title: "无法确认是否可以开始审查",
        tone: "attention",
        reason: "服务端尚未返回当前材料与依赖的准备清单；未知状态按不可开始处理。",
        missingItems: ["服务端审查准备清单"],
        owner: "经办或审查员",
        action: { kind: "refresh-readiness", label: "读取准备清单" },
      };
    }

    const scope = readiness.scopes.find((entry) => entry.id === selectedScope);
    if (!scope) {
      return {
        stage: "审查范围未返回",
        title: "当前审查范围无法核对",
        tone: "attention",
        reason: "准备清单没有返回当前选择的审查范围，不能按其他范围的状态开始。",
        missingItems: [selectedScope],
        owner: "案件管理员",
        action: { kind: "link", href: "#review-readiness", label: "查看准备清单" },
      };
    }

    if (scope.blockers.length > 0 || !scope.can_start || scope.status !== "ready") {
      const firstBlocker = scope.blockers[0];
      if (scope.blockers.length === 0) {
        return {
          stage: "准备状态不完整",
          title: "服务端未确认该范围可以开始",
          tone: "attention",
          reason: `当前范围状态为“${scope.status || "未报告"}”，缺少可执行确认；未知状态按不可开始处理。`,
          missingItems: ["该审查范围的明确可执行状态"],
          owner: "案件管理员",
          action: { kind: "link", href: "#review-readiness", label: "查看完整准备状态" },
        };
      }
      return {
        stage: "审查准备未完成",
        title: "先处理服务端列出的缺项",
        tone: "attention",
        reason: firstBlocker?.next_action || "处理缺项后重新核对准备状态。",
        missingItems: scope.blockers.map((blocker) => blocker.message || blocker.code || "服务端报告的准备缺项"),
        owner: firstBlocker?.code === "case_not_writable" ? "有案件写入权限的经办或管理员" : "经办或案件管理员",
        action: { kind: "link", ...readinessAction(firstBlocker?.code ?? "", item.id) },
      };
    }

    const changeReasons = newReviewReason(item);
    if (changeReasons.length > 0) {
      if (!canStart) {
        return {
          stage: "需要复审",
          title: "等待有权人员发起新的审查",
          tone: "attention",
          reason: `${changeReasons.join("；")}。新审查会保留旧运行、决定和报告。`,
          missingItems: ["由具备发起权限的经办、审查员或管理员开始复审"],
          owner: "经办、审查员或管理员",
          action: { kind: "link", href: "#review-readiness", label: "查看复审准备状态" },
        };
      }
      return {
        stage: "需要复审",
        title: "当前信息与最近一次审查不同",
        tone: "attention",
        reason: `${changeReasons.join("；")}。发起新审查会冻结当前信息，旧运行、决定和报告会保留。`,
        missingItems: [],
        owner: "经办或审查员",
        action: { kind: "start", label: "开始复审", loading: startPending, disabled: startPending },
      };
    }

    if (!canStart) {
      return {
        stage: "准备就绪，等待有权人员操作",
        title: "当前账号没有发起审查权限",
        tone: "neutral",
        reason: "准备清单已就绪；请由具备发起权限的经办、审查员或管理员提交。服务端仍会校验最终权限。",
        missingItems: [],
        owner: "经办、审查员或管理员",
        action: { kind: "link", href: "#review-readiness", label: "查看审查准备状态" },
      };
    }

    return {
      stage: "准备就绪",
      title: run ? "可以开始新的审查" : "可以开始首次审查",
      tone: "good",
      reason: "所选范围已由服务端准备清单确认；受理时服务端会再次核验权限、材料版本和依赖。",
      missingItems: [],
      owner: "经办或审查员",
      action: { kind: "start", label: run ? "开始复审" : "开始审查", loading: startPending, disabled: startPending },
    };
  }

  if (!run) {
    return {
      stage: "审查尚未开始",
      title: "还没有可查看的审查结果",
      tone: "attention",
      reason: "没有运行记录时不能据此判断合同无风险。",
      missingItems: ["首次审查运行"],
      owner: "经办或审查员",
      action: { kind: "link", href: "#review-readiness", label: "查看首次审查准备状态" },
    };
  }

  if (newReviewReason(item).length > 0) {
    return {
      stage: "当前信息已有变化",
      title: "下一次结论需要基于新审查",
      tone: "attention",
      reason: `${newReviewReason(item).join("；")}。先完成本次运行的人工判断；新审查会单独保留历史记录。`,
      missingItems: [],
      owner: "审查员",
      action: { kind: "link", href: reviewHref, label: "打开最近审查处理决定" },
    };
  }

  if (Object.hasOwn(INCOMPLETE_RUN_STATES, run.state)) {
    return {
      stage: runLabel(run.state),
      title: "请在原审查中查看或恢复执行",
      tone: "attention",
      reason: "失败、部分完成或中断的运行应在同一审查工作区处理，避免另开运行拆散已有决定。",
      missingItems: ["本次运行的完整结果"],
      owner: "审查员",
      action: { kind: "link", href: reviewHref, label: "返回原审查工作区" },
    };
  }

  const undecided = item.finding_counts?.undecided;
  if (undecided === undefined || item.finding_counts === null) {
    return {
      stage: "人工判断状态未知",
      title: "服务器没有返回待裁决数量",
      tone: "attention",
      reason: "无法确认是否所有问题都已有人工决定；审查结果或模型建议不能替代人工判断。",
      missingItems: ["待裁决问题数量"],
      owner: "审查员",
      action: { kind: "link", href: reviewHref, label: "查看原文与人工决定" },
    };
  }

  if (undecided > 0 || run.state === "awaiting_human") {
    return {
      stage: "待人工决定",
      title: undecided > 0 ? `还有 ${undecided} 项问题待人工判断` : "运行等待审查员确认",
      tone: "attention",
      reason: "请逐项核对冻结原文、已有证据和规则结果，再保存人工决定；模型建议不会自动成为决定。",
      missingItems: undecided > 0 ? [`${undecided} 项问题尚未完成决定`] : ["本次运行的人工确认"],
      owner: "审查员",
      action: { kind: "link", href: reviewHref, label: "核对原文并处理决定" },
    };
  }

  const unknownRemediation = item.remediations.find((entry) => !Object.hasOwn(KNOWN_REMEDIATION_STATES, entry.status));
  if (unknownRemediation) {
    return {
      stage: "整改状态未识别",
      title: "暂不能确认是否可以生成最终报告",
      tone: "attention",
      reason: `整改记录 ${unknownRemediation.id} 返回了未识别状态“${unknownRemediation.status || "空"}”；请先核对整改记录。`,
      missingItems: ["可识别的整改状态"],
      owner: "整改负责人或审查员",
      action: { kind: "link", href: "#remediation-title", label: "查看整改记录" },
    };
  }

  const submitted = item.remediations.find((entry) => entry.status === "submitted");
  const openRemediation = item.remediations.find((entry) => entry.status === "open" || entry.status === "reopened");
  const activeRemediation = submitted ?? openRemediation;
  if (activeRemediation) {
    if (historyLoading) {
      return {
        stage: "正在读取整改复核依据",
        title: "等待审查与执行历史",
        tone: "progress",
        reason: "整改提交和复核需要引用对应的来源审查与执行记录。",
        missingItems: ["审查与执行历史"],
        owner: activeRemediation.status === "submitted" ? "审查员" : remediationOwner(item, activeRemediation),
        action: { kind: "refresh-history", label: "正在读取审查历史", loading: true, disabled: true },
      };
    }
    if (historyError) {
      return {
        stage: "整改复核依据不可用",
        title: "审查历史没有加载",
        tone: "attention",
        reason: `无法读取来源审查和执行记录：${errorMessage(historyError)}`,
        missingItems: ["整改关联的来源审查和执行记录"],
        owner: activeRemediation.status === "submitted" ? "审查员" : remediationOwner(item, activeRemediation),
        action: { kind: "refresh-history", label: "重试读取审查历史" },
        announcement: "alert",
      };
    }
    return {
      stage: submitted ? "补件待复核" : activeRemediation.status === "reopened" ? "整改重新打开" : "整改处理中",
      title: submitted ? "经办已提交修改稿，等待复核" : "这项整改尚未关闭",
      tone: "attention",
      reason: submitted
        ? "关闭整改前，需要依据新的结束审查和服务端返回的兼容评估完成复核；服务端会核验来源冻结信息。"
        : "整改记录绑定了来源发现；仅上传材料不会提交或关闭整改。",
      missingItems: submitted
        ? ["新的复核执行记录及服务端兼容确认"]
        : ["修改稿与整改措施的提交"],
      owner: submitted ? "审查员" : remediationOwner(item, activeRemediation),
      action: { kind: "link", href: "#remediation-title", label: submitted ? "打开补件复核记录" : "打开整改记录" },
    };
  }

  if (historyLoading) {
    return {
      stage: "正在读取报告依据",
      title: "等待审查与执行历史",
      tone: "progress",
      reason: "生成报告需要选择已完成的审查与执行记录。",
      missingItems: ["可用于报告的审查与执行历史"],
      owner: "经办",
      action: { kind: "refresh-history", label: "正在读取审查历史", loading: true, disabled: true },
    };
  }
  if (historyError) {
    return {
      stage: "报告依据不可用",
      title: "审查历史没有加载",
      tone: "attention",
      reason: `无法读取用于报告的审查与执行记录：${errorMessage(historyError)}`,
      missingItems: ["可用于报告的审查与执行历史"],
      owner: "经办",
      action: { kind: "refresh-history", label: "重试读取审查历史" },
      announcement: "alert",
    };
  }

  if (item.reports.length > 0) {
    return {
      stage: "审查与人工决定已保存",
      title: canCreateReport ? "报告可查看或导出" : "报告已保存，需由经办导出",
      tone: "good",
      reason: canCreateReport
        ? "报告保留所选审查当时的内容；材料更新不会改写已生成版本。"
        : "当前角色没有报告读取权限，请由经办或管理员查看并导出。",
      missingItems: [],
      owner: canCreateReport ? "经办" : "经办或管理员",
      action: { kind: "link", href: "#report-archive-title", label: "进入报告与导出入口" },
    };
  }

  return {
    stage: "待生成审查报告",
    title: canCreateReport ? "人工判断已完成，选择报告依据" : "等待经办生成审查报告",
    tone: "neutral",
    reason: canCreateReport
      ? "请在报告区选择本次审查与执行记录；服务端会核验冻结输入、规则依据和结果。符合条件后生成固定版本，再从同一区域导出。"
      : "当前角色不能生成报告；请交由经办或管理员完成报告保存和导出。",
    missingItems: ["审查报告"],
    owner: canCreateReport ? "经办" : "经办或管理员",
    action: { kind: "link", href: "#report-archive-title", label: "进入报告生成与导出区" },
  };
}

export function CaseNextStep(props: Props) {
  const plan = planFor(props);
  const { actorRole } = props;
  const action = plan.action;
  const actionProps = { className: "case-next-step-button", "aria-label": action.label, type: "primary" as const, htmlType: "button" as const, loading: action.loading, disabled: action.disabled };

  let mainAction;
  if (action.kind === "link") {
    mainAction = <Button {...actionProps} href={action.href}>{action.label}</Button>;
  } else if (action.kind === "start") {
    mainAction = <Button {...actionProps} onClick={props.onStartReview}>{action.label}</Button>;
  } else if (action.kind === "refresh-session") {
    mainAction = <Button {...actionProps} onClick={props.onRetrySession}>{action.label}</Button>;
  } else if (action.kind === "refresh-readiness") {
    mainAction = <Button {...actionProps} onClick={props.onRefreshReadiness}>{action.label}</Button>;
  } else {
    mainAction = <Button {...actionProps} onClick={props.onRefreshHistory}>{action.label}</Button>;
  }

  return (
    <section className="workflow-panel case-next-step" aria-labelledby="case-next-step-title">
      <header className="workflow-panel-head case-next-step-head">
        <div>
          <p className="case-next-step-eyebrow">当前阶段</p>
          <h2 id="case-next-step-title">{plan.title}</h2>
        </div>
        <StatusText tone={plan.tone}>{plan.stage}</StatusText>
      </header>
      <div className="case-next-step-grid">
        <section aria-labelledby="case-next-step-reason-title">
          <h3 id="case-next-step-reason-title">缺项与原因</h3>
          {plan.missingItems.length ? (
            <ul>
              {plan.missingItems.map((missingItem, index) => <li key={`${index}:${missingItem}`}>{missingItem}</li>)}
            </ul>
          ) : <p>当前阶段没有额外缺项。</p>}
          <p className="case-next-step-reason" role={plan.announcement}>{plan.reason}</p>
        </section>
        <section aria-labelledby="case-next-step-owner-title">
          <h3 id="case-next-step-owner-title">负责角色或人员</h3>
          <p>{plan.owner}</p>
        </section>
      </div>
      <section className="case-next-step-focus" aria-label={`${actorRoleLabel(actorRole)}工作重点`}>
        <strong>{actorRoleLabel(actorRole)}工作重点</strong>
        <p>{roleFocus(actorRole)}</p>
      </section>
      <div className="case-next-step-action">
        <div>
          <strong>唯一主要下一步</strong>
          <p>页面提示用于定位当前工作；提交时仍以服务端校验结果为准。</p>
        </div>
        {mainAction}
      </div>
    </section>
  );
}
