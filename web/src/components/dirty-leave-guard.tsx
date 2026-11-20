// File metadata (manifest mirror):
// Purpose: Renders DirtyLeaveGuard, the dirty leave guard workbench UI, from the selected case/workspace projection and the module’s query/state inputs; it displays server-derived state and does not establish business authorization or evidence validity.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-31
// Updated: 2026-11-15
// Revision: r3
// Copyright: See LICENSE
// End file metadata.

import { Modal } from "antd";
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useLayoutEffect,
  useRef,
  useState,
  type ReactNode,
} from "react";
import { useBlocker } from "react-router";

type Draft = { description?: string; onDiscard?: () => void };
type GuardProps = Draft & { dirty: boolean };
type RegisterDraft = (draft: Draft) => () => void;

const DirtyLeaveContext = createContext<RegisterDraft | null>(null);
const defaultDescription =
  "当前页面有未提交的草稿。留在本页可以继续编辑；离开后草稿会保留，但关闭或刷新浏览器标签页会丢失。";

export function DirtyLeaveGuardProvider({ children }: { children: ReactNode }) {
  const drafts = useRef(new Set<Draft>());
  const register = useCallback<RegisterDraft>((draft) => {
    const current = drafts.current;
    current.add(draft);
    return () => current.delete(draft);
  }, []);
  const [descriptions, setDescriptions] = useState<string[]>([]);
  const blocker = useBlocker(({ currentLocation, nextLocation }) => {
    if (currentLocation.pathname === nextLocation.pathname || drafts.current.size === 0)
      return false;
    setDescriptions(
      Array.from(
        new Set(Array.from(drafts.current, ({ description }) => description ?? defaultDescription)),
      ),
    );
    return true;
  });

  useEffect(() => {
    const onBeforeUnload = (event: BeforeUnloadEvent) => {
      if (drafts.current.size === 0) return;
      event.preventDefault();
      event.returnValue = "";
    };
    window.addEventListener("beforeunload", onBeforeUnload);
    return () => window.removeEventListener("beforeunload", onBeforeUnload);
  }, []);

  return (
    <DirtyLeaveContext.Provider value={register}>
      {children}
      <Modal
        cancelText="留在本页"
        closable={false}
        keyboard={false}
        mask={{ closable: false }}
        okButtonProps={{ danger: true }}
        okText="离开"
        onCancel={() => {
          if (blocker.state === "blocked") blocker.reset();
        }}
        onOk={() => {
          if (blocker.state !== "blocked") return;
          for (const { onDiscard } of Array.from(drafts.current)) onDiscard?.();
          blocker.proceed();
        }}
        open={blocker.state === "blocked"}
        title="有未提交的草稿"
      >
        {descriptions.map((description) => (
          <p key={description}>{description}</p>
        ))}
      </Modal>
    </DirtyLeaveContext.Provider>
  );
}

function RegisterDirtyDraft({ dirty, description, onDiscard }: GuardProps) {
  const register = useContext(DirtyLeaveContext);
  useLayoutEffect(() => {
    if (dirty) return register?.({ description, onDiscard });
  }, [description, dirty, onDiscard, register]);
  return null;
}

export function DirtyLeaveGuard(props: GuardProps) {
  const register = useContext(DirtyLeaveContext);
  if (register) return <RegisterDirtyDraft {...props} />;
  return (
    <DirtyLeaveGuardProvider>
      <RegisterDirtyDraft {...props} />
    </DirtyLeaveGuardProvider>
  );
}
