// File metadata (manifest mirror):
// Purpose: Hosts case intake as a route or dialog and preserves actor isolation, unsettled drafts and navigation after a successful upload.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-20
// Updated: 2026-11-20
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import { Modal } from "antd";
import { useCallback, useEffect, useState } from "react";
import { useNavigate } from "react-router";
import { useSession } from "../../api/queries";
import { DirtyLeaveGuard } from "../../components/dirty-leave-guard";
import { IntakePrimaryContract } from "../../components/intake-primary-contract";
import { actorReadContextKey } from "../../domain/actor-read-context";
import { caseHref } from "../../domain/review";
import "./review-intake-dialog.css";

type IntakeSurfaceProps = { onClose?: () => void };

export function ReviewIntakeSurface({ onClose }: IntakeSurfaceProps) {
  const session = useSession();
  const actor = session.isSuccess ? session.data : null;
  const scope = actorReadContextKey(actor);
  return <ScopedReviewIntake key={scope} actor={actor} onClose={onClose} />;
}

function ScopedReviewIntake({
  actor,
  onClose,
}: IntakeSurfaceProps & { actor: ReturnType<typeof useSession>["data"] | null }) {
  const navigate = useNavigate();
  const [state, setState] = useState({ dirty: false, busy: false });
  const [confirmCancel, setConfirmCancel] = useState(false);
  const [destination, setDestination] = useState<string | null>(null);
  const onStateChange = useCallback((dirty: boolean, busy: boolean) => {
    setState({ dirty, busy });
  }, []);

  const cancel = useCallback(() => {
    if (state.busy) return;
    if (state.dirty) {
      setConfirmCancel(true);
      return;
    }
    if (onClose) onClose();
    else void navigate("/cases");
  }, [navigate, onClose, state.busy, state.dirty]);

  useEffect(() => {
    if (!destination) return;
    void navigate(destination);
    onClose?.();
  }, [destination, navigate, onClose]);

  const form = (
    <>
      <IntakePrimaryContract
        actor={actor ?? null}
        sheet={{
          onComplete: (caseId) => setDestination(caseHref(caseId)),
          onCancel: cancel,
          onStateChange,
        }}
      />
      {confirmCancel ? (
        <Modal
          open
          title="放弃未提交的材料？"
          cancelText="继续填写"
          okText="放弃并返回"
          okButtonProps={{ danger: true }}
          onCancel={() => setConfirmCancel(false)}
          onOk={() => {
            setConfirmCancel(false);
            if (onClose) onClose();
            else {
              setDestination("/cases");
            }
          }}
        >
          <p>尚未提交的文件和填写内容会丢失。已建立的案件和上传回执会保留。</p>
        </Modal>
      ) : null}
    </>
  );

  if (onClose) {
    return (
      <Modal
        open
        centered
        width={520}
        title="审一份合同"
        footer={null}
        mask={{ closable: false }}
        closable={!state.busy}
        keyboard={!state.busy}
        onCancel={cancel}
        classNames={{ container: "review-intake-modal" }}
      >
        {form}
        <DirtyLeaveGuard
          dirty={state.dirty && !destination}
          description="尚未提交的文件和填写内容会丢失。已建立的案件和上传回执会保留。"
          onDiscard={onClose}
        />
      </Modal>
    );
  }

  return (
    <>
      {form}
      <DirtyLeaveGuard
        dirty={state.dirty && !destination}
        description="尚未提交的文件和填写内容会丢失。已建立的案件和上传回执会保留。"
      />
    </>
  );
}

export function ReviewIntakeDialog({ open, onClose }: { open: boolean; onClose: () => void }) {
  return open ? <ReviewIntakeSurface onClose={onClose} /> : null;
}
