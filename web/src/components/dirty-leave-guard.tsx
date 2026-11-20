// File metadata (manifest mirror):
// Purpose: Registers pending edits with a single router blocker so that a page and its intake dialog share leave and reload protection.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-10-31
// Updated: 2026-11-20
// Revision: r4
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

type Draft = { description?: string; onDiscard?: () => void; blockSearchChange?: boolean };
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
  const blockedDrafts = useRef<Draft[]>([]);
  const blocker = useBlocker(({ currentLocation, nextLocation }) => {
    const leavesPage = currentLocation.pathname !== nextLocation.pathname;
    const changesSearch = currentLocation.search !== nextLocation.search;
    const affected = Array.from(drafts.current).filter(
      (draft) => leavesPage || (changesSearch && draft.blockSearchChange),
    );
    if (affected.length === 0) return false;
    blockedDrafts.current = affected;
    setDescriptions(
      Array.from(new Set(affected.map(({ description }) => description ?? defaultDescription))),
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
          if (blocker.state !== "blocked") return;
          blockedDrafts.current = [];
          blocker.reset();
        }}
        onOk={() => {
          if (blocker.state !== "blocked") return;
          const affected = blockedDrafts.current;
          blockedDrafts.current = [];
          for (const { onDiscard } of affected) onDiscard?.();
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

function RegisterDirtyDraft({ dirty, description, onDiscard, blockSearchChange }: GuardProps) {
  const register = useContext(DirtyLeaveContext);
  useLayoutEffect(() => {
    if (dirty) return register?.({ description, onDiscard, blockSearchChange });
  }, [blockSearchChange, description, dirty, onDiscard, register]);
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
