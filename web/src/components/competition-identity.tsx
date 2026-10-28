export function CompetitionIdentity() {
  return (
    <>
      <img
        className="competition-logo"
        src={`${import.meta.env.BASE_URL}huangjueshu-competition-logo.png?v=20261028`}
        alt=""
        width={38}
        height={38}
      />
      <span className="brand-copy">
        <strong>第一届黄桷树AI智能体开发大赛</strong>
        <span>合同智能审计工作台</span>
      </span>
    </>
  );
}
