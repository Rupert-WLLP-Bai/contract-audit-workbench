import { Modal } from "antd";
import { useEffect } from "react";
import { useBlocker } from "react-router";

// Router-level blocking only intercepts in-app SPA navigation; tab close and
// refresh are covered by the beforeunload listener below. Both stay inert
// until `dirty` becomes true. Drafts live in memory, so SPA navigation keeps
// them and only closing or reloading the tab loses them.
export function DirtyLeaveGuard({
  dirty,
  description,
}: {
  dirty: boolean;
  description?: string;
}) {
  const blocker = useBlocker(
    ({ currentLocation, nextLocation }) =>
      dirty && currentLocation.pathname !== nextLocation.pathname,
  );

  useEffect(() => {
    if (!dirty) return;
    const onBeforeUnload = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = "";
    };
    window.addEventListener("beforeunload", onBeforeUnload);
    return () => window.removeEventListener("beforeunload", onBeforeUnload);
  }, [dirty]);

  return (
    <Modal
      cancelText="留在本页"
      closable={false}
      keyboard={false}
      maskClosable={false}
      okButtonProps={{ danger: true }}
      okText="离开"
      onCancel={() => {
        if (blocker.state === "blocked") blocker.reset();
      }}
      onOk={() => {
        if (blocker.state === "blocked") blocker.proceed();
      }}
      open={blocker.state === "blocked"}
      title="有未提交的草稿"
    >
      {description ??
        "当前页面有未提交的草稿。留在本页可以继续编辑；离开后草稿会保留，但关闭或刷新浏览器标签页会丢失。"}
    </Modal>
  );
}
