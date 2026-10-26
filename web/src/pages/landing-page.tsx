import {
  ArrowRightOutlined,
  AuditOutlined,
  BookOutlined,
  BulbOutlined,
  CheckCircleFilled,
  DatabaseOutlined,
  FileDoneOutlined,
  FileSearchOutlined,
  FileTextOutlined,
  LockOutlined,
  SafetyCertificateOutlined,
  ScanOutlined,
  TeamOutlined,
  UploadOutlined,
} from "@ant-design/icons";
import { Button } from "antd";
import { Link, useNavigate } from "react-router";
import "./landing-page.css";

const CAPABILITIES = [
  {
    icon: <FileSearchOutlined />,
    tone: "red",
    title: "智能风险识别",
    body: "结合规则命中与模型建议，按严重程度汇总条款风险，并保留每条结论的来源。",
  },
  {
    icon: <BookOutlined />,
    tone: "blue",
    title: "专业规则包",
    body: "以业务规则包和规范知识库为基线，审查口径由组织治理，可复核、可版本化。",
  },
  {
    icon: <BulbOutlined />,
    tone: "green",
    title: "审查建议生成",
    body: "围绕风险给出可执行的修改方向；模型建议需经人工确认后才成为审查结论。",
  },
  {
    icon: <FileTextOutlined />,
    tone: "amber",
    title: "多格式文档支持",
    body: "支持 TXT、Markdown、PDF、DOCX 与图片材料，解析结果按审查尝试冻结。",
  },
];

const STEPS = [
  {
    icon: <UploadOutlined />,
    title: "上传与解析",
    body: "登记案件材料，冻结本次审查的输入与解析快照，后续解析不覆盖历史。",
  },
  {
    icon: <ScanOutlined />,
    title: "规则与模型审查",
    body: "规则核验与模型分析并行产出发现项，并为结论定位可核对的原文证据。",
  },
  {
    icon: <FileDoneOutlined />,
    title: "人工复核与报告",
    body: "逐条作出人工决定，生成带版本、摘要与决定快照的冻结审查报告。",
  },
];

const SCENARIOS = [
  {
    icon: <SafetyCertificateOutlined />,
    tone: "blue",
    title: "政企专线",
    body: "核对带宽、SLA、结算与违约条款，降低专线履约与交付风险。",
    tags: ["服务合同", "采购", "SLA"],
  },
  {
    icon: <DatabaseOutlined />,
    tone: "green",
    title: "工程采购",
    body: "对齐工程量、验收标准、付款节点与质保责任，减少结算争议。",
    tags: ["网络工程", "验收", "付款"],
  },
  {
    icon: <TeamOutlined />,
    tone: "amber",
    title: "网络代维",
    body: "梳理维护边界、响应时限与考核扣罚，让代维责任清晰可执行。",
    tags: ["代维服务", "响应时限", "合规"],
  },
];

const SECURITY = [
  {
    icon: <LockOutlined />,
    tone: "blue",
    title: "权限与范围",
    body: "案件授权默认拒绝，租户、组织、角色与数据范围由服务端解析。",
  },
  {
    icon: <AuditOutlined />,
    tone: "green",
    title: "审计留痕",
    body: "审查输入、人工决定与报告按版本冻结，关键操作写入审计日志。",
  },
  {
    icon: <DatabaseOutlined />,
    tone: "amber",
    title: "私有化部署",
    body: "支持组织内网部署与自有模型服务，业务数据不必离开企业边界。",
  },
  {
    icon: <SafetyCertificateOutlined />,
    tone: "red",
    title: "数据与工具边界",
    body: "工具网关保持只读，外部写回必须经过人工授权，模型密钥不进入页面。",
  },
] as const;

function scrollTo(id: string) {
  document.getElementById(id)?.scrollIntoView({ behavior: "smooth" });
}

export function LandingPage() {
  const navigate = useNavigate();
  const year = new Date().getFullYear();

  return (
    <div className="landing">
      <a className="landing-skip" href="#landing-main">
        跳到主要内容
      </a>
      <header className="landing-nav">
        <Link className="landing-brand" to="/" aria-label="合同智能审计首页">
          <span className="landing-brand__mark" aria-hidden="true">
            <SafetyCertificateOutlined />
          </span>
          <span className="landing-brand__copy">
            <strong>合同智能审计</strong>
            <small>让合同更安全，让业务更稳健</small>
          </span>
        </Link>
        <nav className="landing-nav__links" aria-label="产品导航">
          <a href="#capabilities">产品能力</a>
          <a href="#scenarios">适用场景</a>
          <a href="#workflow">审查流程</a>
          <a href="#security">安全合规</a>
          <a href="#contact">联系我们</a>
        </nav>
        <div className="landing-nav__actions">
          <Button onClick={() => void navigate("/login")}>登录</Button>
          <Button
            className="landing-nav__primary"
            type="primary"
            onClick={() => void navigate("/dashboard")}
          >
            进入工作台
          </Button>
        </div>
      </header>

      <main id="landing-main">
        <section className="landing-hero" aria-labelledby="landing-hero-title">
          <div className="landing-hero__copy">
            <span className="landing-eyebrow">
              <span className="landing-eyebrow__dot" aria-hidden="true" />
              基于大模型的合同风险审查平台
            </span>
            <h1 id="landing-hero-title">
              让合同审查
              <br />
              <em>更智能、更高效</em>
            </h1>
            <p className="landing-hero__lead">
              以规则与模型协同识别条款风险，逐条给出原文依据，把人工复核与报告生成留在同一条可追溯的审查链路里。
            </p>
            <div className="landing-hero__actions">
              <Button
                type="primary"
                onClick={() => void navigate("/dashboard")}
              >
                立即体验 <ArrowRightOutlined />
              </Button>
              <Button onClick={() => scrollTo("capabilities")}>
                查看方案
              </Button>
            </div>
            <ul className="landing-hero__facts" aria-label="产品能力要点">
              <li>
                <strong>
                  <CheckCircleFilled aria-hidden="true" /> 原文定位
                </strong>
                <span>风险可回溯到冻结原文</span>
              </li>
              <li>
                <strong>
                  <CheckCircleFilled aria-hidden="true" /> 规则 + 模型
                </strong>
                <span>两类判断分别标注来源</span>
              </li>
              <li>
                <strong>
                  <CheckCircleFilled aria-hidden="true" /> 人工复核
                </strong>
                <span>决定追加留痕，不被覆盖</span>
              </li>
            </ul>
          </div>

          <div
            className="landing-stage"
            role="img"
            aria-label="产品界面示意：左侧合同原文中的风险条款被高亮，右侧列出对应风险与原文依据。"
          >
            <div className="landing-stage__glow" aria-hidden="true" />
            <div className="landing-mock" aria-hidden="true">
              <div className="landing-mock__bar">
                <span className="landing-mock__dot" />
                <span className="landing-mock__dot" />
                <span className="landing-mock__dot" />
                <span className="landing-mock__brand">
                  <i /> 合同智能审计
                </span>
                <span className="landing-mock__search" />
              </div>
              <div className="landing-mock__body">
                <div className="landing-mock__nav">
                  <span />
                  <span />
                  <span />
                  <span />
                  <span />
                </div>
                <div className="landing-mock__doc">
                  <h3>合同原文</h3>
                  <span className="landing-mock__caption">第一条 服务内容</span>
                  <div className="landing-mock__line" />
                  <div className="landing-mock__line landing-mock__line--mid" />
                  <div className="landing-mock__line landing-mock__line--short" />
                  <span className="landing-mock__caption">第二条 付款方式</span>
                  <div className="landing-mock__line landing-mock__line--risk" />
                  <div className="landing-mock__line" />
                  <div className="landing-mock__line landing-mock__line--mid" />
                  <div className="landing-mock__line landing-mock__line--short" />
                </div>
                <div className="landing-mock__result">
                  <h3>风险与依据</h3>
                  {[
                    { color: "#EF4444", label: "高" },
                    { color: "#F59E0B", label: "中" },
                    { color: "#10B981", label: "低" },
                  ].map((risk) => (
                    <div className="landing-mock__risk" key={risk.label}>
                      <i style={{ background: risk.color }} />
                      <span className="landing-mock__risk-level">
                        {risk.label}
                      </span>
                      <b />
                    </div>
                  ))}
                  <div className="landing-mock__evidence">
                    <span>依据</span>
                    <div className="landing-mock__line" />
                    <div className="landing-mock__line landing-mock__line--short" />
                  </div>
                </div>
              </div>
            </div>
            <span className="landing-float landing-float--pdf">PDF</span>
            <span className="landing-float landing-float--docx">DOC</span>
            <span className="landing-float landing-float--md">MD</span>
            <div className="landing-suggestion" aria-hidden="true">
              <strong>
                <BulbOutlined /> 复核提示
              </strong>
              <p>点击风险项可定位原文；模型建议需经人工确认后才会写入结论。</p>
            </div>
            <span className="landing-stage__note">界面示意 · 数据以实际系统为准</span>
          </div>
        </section>

        <section
          className="landing-section"
          id="capabilities"
          aria-labelledby="capabilities-title"
        >
          <div className="landing-section__head landing-section__head--center">
            <h2 id="capabilities-title">核心产品能力</h2>
            <p>覆盖合同审查全流程，让每一次判断都有依据、可复核。</p>
          </div>
          <div className="landing-capabilities">
            {CAPABILITIES.map((item) => (
              <article className="landing-capability" key={item.title}>
                <span
                  className={`landing-icon-tile landing-icon-tile--${item.tone}`}
                  aria-hidden="true"
                >
                  {item.icon}
                </span>
                <div className="landing-capability__copy">
                  <h3>{item.title}</h3>
                  <p>{item.body}</p>
                  <a className="landing-link" href="#workflow">
                    了解流程 <ArrowRightOutlined aria-hidden="true" />
                  </a>
                </div>
              </article>
            ))}
          </div>
        </section>

        <section
          className="landing-section"
          id="workflow"
          aria-labelledby="workflow-title"
        >
          <div className="landing-section__head">
            <h2 id="workflow-title">三步完成合同审查</h2>
            <p>简单清晰，让专业审查能力进入日常业务流程。</p>
          </div>
          <div className="landing-flow">
            {STEPS.map((step, index) => (
              <article className="landing-step" key={step.title}>
                <div className="landing-step__rail">
                  <span className="landing-step__index" aria-hidden="true">
                    {index + 1}
                  </span>
                  <span className="landing-step__icon" aria-hidden="true">
                    {step.icon}
                  </span>
                </div>
                <div className="landing-step__copy">
                  <h3>{step.title}</h3>
                  <p>{step.body}</p>
                </div>
              </article>
            ))}
          </div>
        </section>

        <section
          className="landing-section"
          id="scenarios"
          aria-labelledby="scenarios-title"
        >
          <div className="landing-split">
            <div>
              <div className="landing-section__head">
                <h2 id="scenarios-title">适用多种业务场景</h2>
                <p>围绕运营商与企业合同场景，沉淀可复用的审查口径。</p>
              </div>
              <div className="landing-scenarios">
                {SCENARIOS.map((item) => (
                  <article className="landing-scenario" key={item.title}>
                    <span
                      className={`landing-icon-tile landing-icon-tile--${item.tone}`}
                      aria-hidden="true"
                    >
                      {item.icon}
                    </span>
                    <div>
                      <h3>{item.title}</h3>
                      <p>{item.body}</p>
                      <div className="landing-tags">
                        {item.tags.map((tag) => (
                          <span key={tag}>{tag}</span>
                        ))}
                      </div>
                    </div>
                  </article>
                ))}
              </div>
            </div>
            <div id="security">
              <div className="landing-section__head">
                <h2>企业级安全保障</h2>
                <p>多重安全机制，守住企业合同数据与审查结论的边界。</p>
              </div>
              <div className="landing-security">
                {SECURITY.map((item) => (
                  <article className="landing-security__item" key={item.title}>
                    <span
                      className={`landing-icon-tile landing-icon-tile--${item.tone}`}
                      aria-hidden="true"
                    >
                      {item.icon}
                    </span>
                    <h3>{item.title}</h3>
                    <p>{item.body}</p>
                  </article>
                ))}
              </div>
            </div>
          </div>
        </section>

        <section className="landing-cta" id="contact" aria-labelledby="cta-title">
          <div>
            <h2 id="cta-title">开始更可靠的合同审查</h2>
            <p>从一份合同开始，把风险识别、原文依据与人工复核连接起来。</p>
          </div>
          <div className="landing-cta__actions">
            <Button
              type="primary"
              onClick={() => void navigate("/dashboard")}
            >
              进入工作台 <ArrowRightOutlined />
            </Button>
            <Button onClick={() => void navigate("/login")}>登录系统</Button>
          </div>
        </section>
      </main>

      <footer className="landing-footer">
        <div className="landing-footer__inner">
          <Link className="landing-brand" to="/" aria-label="合同智能审计首页">
            <span className="landing-brand__mark" aria-hidden="true">
              <SafetyCertificateOutlined />
            </span>
            <span className="landing-brand__copy">
              <strong>合同智能审计</strong>
              <small>规则与模型协同的可追溯审查</small>
            </span>
          </Link>
          <nav className="landing-footer__links" aria-label="页脚导航">
            <a href="#capabilities">产品能力</a>
            <a href="#workflow">审查流程</a>
            <a href="#security">安全合规</a>
            <Link to="/login">登录</Link>
          </nav>
          <span className="landing-footer__copy">
            © {year} 合同智能审计工作台
          </span>
        </div>
      </footer>
    </div>
  );
}
