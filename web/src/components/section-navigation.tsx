// File metadata (manifest mirror):
// Purpose: Coordinates page-scoped section registration, controlled ancestor expansion, hash navigation and scoped focus/scroll requests without storing document or form contents.
// Author: Bai Junhao <baijunhao@cq.chinamobile.com>
// Contact: +86 13617696321
// Created: 2026-11-05
// Updated: 2026-11-05
// Revision: r1
// Copyright: See LICENSE
// End file metadata.

import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useLayoutEffect,
  useMemo,
  useRef,
  useState,
  type MouseEvent,
  type ReactNode,
  type RefObject,
} from "react";
import { useLocation, useNavigate, useNavigationType } from "react-router";

type OpenSection = () => void;
interface SectionNavigation {
  register: (element: HTMLDetailsElement, open: OpenSection) => () => void;
}

const SectionNavigationContext = createContext<SectionNavigation | null>(null);

export function useSectionNavigationAvailable(): boolean {
  return useContext(SectionNavigationContext) !== null;
}

/** Ignore retry/loading controls outside the currently readable page content. */
export function isReadingInteraction(event: Event, root: HTMLElement | null): boolean {
  if (!root?.isConnected || root.closest("[hidden], [aria-hidden='true']")) return false;
  const target = event.target;
  if (target === window || target === document || target === document.body || target === document.documentElement) return true;
  if (!(target instanceof Node) || !root.contains(target)) return false;
  const element = target instanceof Element ? target : target.parentElement;
  return !element?.closest(".page-status");
}

export interface CaseListReturn {
  returnTo: string;
  scrollY: number;
  selectedId: string;
  actorScope: string;
}

/** Only the existing list URL and a position may cross back to that list. */
export function readCaseListReturn(state: unknown, caseId: string, actorScope: string): CaseListReturn | null {
  if (!state || typeof state !== "object" || !actorScope) return null;
  const value = state as Partial<CaseListReturn>;
  if (typeof value.returnTo !== "string" || !value.returnTo.startsWith("/cases?") ||
      value.returnTo.includes("\\") || value.returnTo.length > 4096 || value.actorScope !== actorScope ||
      value.selectedId !== caseId || typeof value.scrollY !== "number" || !Number.isFinite(value.scrollY) || value.scrollY < 0) return null;
  try {
    const url = new URL(value.returnTo, "https://navigation.invalid");
    if (url.origin !== "https://navigation.invalid" || url.pathname !== "/cases" || url.hash || url.searchParams.get("selected") !== caseId) return null;
    const safe = new URLSearchParams();
    for (const key of ["q", "run", "business_status", "sort", "direction", "page", "page_size", "selected"]) {
      const param = url.searchParams.get(key);
      if (param !== null && param.length <= 512 && !/[\u0000-\u001f]/.test(param)) safe.set(key, param);
    }
    if (safe.get("selected") !== caseId) return null;
    return { returnTo: `${url.pathname}?${safe}`, scrollY: value.scrollY, selectedId: caseId, actorScope };
  } catch {
    return null;
  }
}

export interface CaseReadingPosition {
  caseId: string;
  actorScope: string;
  scrollY: number;
}

export type CaseReturnState = Partial<CaseListReturn> & { caseReadingPosition?: CaseReadingPosition };

export function readCaseReadingPosition(state: unknown, caseId: string, actorScope: string): CaseReadingPosition | null {
  if (!state || typeof state !== "object" || !("caseReadingPosition" in state)) return null;
  const value = state.caseReadingPosition;
  if (!value || typeof value !== "object" || !("caseId" in value) || !("actorScope" in value) || !("scrollY" in value) ||
      value.caseId !== caseId || !actorScope || value.actorScope !== actorScope ||
      typeof value.scrollY !== "number" || !Number.isFinite(value.scrollY) || value.scrollY < 0) return null;
  return { caseId, actorScope, scrollY: value.scrollY };
}

export interface ReviewReadingPosition {
  caseId: string;
  runId: string;
  attemptId: string | null;
  actorScope: string;
  scrollY: number;
  documentScroll: number;
  findingScroll: number;
}

export function readReviewReadingPosition(state: unknown, caseId: string, runId: string, attemptId: string | null, actorScope: string): ReviewReadingPosition | null {
  if (!state || typeof state !== "object" || !("reviewReadingPosition" in state)) return null;
  const value = state.reviewReadingPosition;
  if (!value || typeof value !== "object") return null;
  const position = value as Partial<ReviewReadingPosition>;
  if (!actorScope || position.actorScope !== actorScope || position.caseId !== caseId || position.runId !== runId || position.attemptId !== attemptId ||
      ![position.scrollY, position.documentScroll, position.findingScroll].every((number) => typeof number === "number" && Number.isFinite(number) && number >= 0)) return null;
  return {
    caseId, runId, attemptId, actorScope,
    scrollY: position.scrollY!, documentScroll: position.documentScroll!, findingScroll: position.findingScroll!,
  };
}

export function reviewPositionForPath(state: unknown, path: string, caseId: string, actorScope: string): ReviewReadingPosition | null {
  try {
    const url = new URL(path, "https://navigation.invalid");
    const parts = url.pathname.split("/");
    if (url.origin !== "https://navigation.invalid" || parts.length !== 5 || parts[1] !== "cases" ||
        decodeURIComponent(parts[2] ?? "") !== caseId || parts[3] !== "reviews") return null;
    return readReviewReadingPosition(state, caseId, decodeURIComponent(parts[4] ?? ""), url.searchParams.get("attempt_id"), actorScope);
  } catch {
    return null;
  }
}

/** Register a controlled details element without changing its normal toggle behavior. */
export function useNavigableSection(ref: RefObject<HTMLDetailsElement | null>, open: OpenSection) {
  const navigation = useContext(SectionNavigationContext);
  useLayoutEffect(() => {
    const element = ref.current;
    return element && navigation ? navigation.register(element, open) : undefined;
  }, [navigation, open, ref]);
}

function fragment(hash: string): string | null {
  if (!hash.startsWith("#") || hash.length === 1) return null;
  try {
    const id = decodeURIComponent(hash.slice(1));
    return id && id.length <= 512 && !/[\u0000-\u001f]/.test(id) ? id : null;
  } catch {
    return null;
  }
}

function focusAndScroll(target: HTMLElement) {
  const focusTarget = target instanceof HTMLDetailsElement
    ? target.querySelector<HTMLElement>(":scope > summary") ?? target
    : target.matches("h1,h2,h3,h4,h5,h6,button,a,input,select,textarea,summary")
      ? target
      : target.querySelector<HTMLElement>("h1,h2,h3,h4,h5,h6,summary,button,a,input,select,textarea") ?? target;
  if (!focusTarget.hasAttribute("tabindex") && !focusTarget.matches("a[href],button,input,select,textarea,summary")) {
    focusTarget.tabIndex = -1;
  }
  focusTarget.focus({ preventScroll: true });
  focusTarget.scrollIntoView?.({ block: "start", behavior: "auto" });
  // The header can wrap at high zoom; use its actual height when it covers focus.
  const header = document.querySelector<HTMLElement>(".topbar");
  const headerRect = header?.getBoundingClientRect();
  const focusRect = focusTarget.getBoundingClientRect();
  if (headerRect && headerRect.bottom > 0 && focusRect.top < headerRect.bottom) {
    window.scrollBy(0, focusRect.top - headerRect.bottom - 12);
  }
}

/** A coordinator for the current page, not a global navigation cache. */
export function SectionNavigationProvider({
  children,
  scope,
  actorScope = scope,
  ready,
  targetPending,
}: {
  children: ReactNode;
  scope: string;
  actorScope?: string;
  ready: boolean;
  targetPending?: (id: string) => boolean;
}) {
  const location = useLocation();
  const navigate = useNavigate();
  const navigationType = useNavigationType();
  const root = useRef<HTMLDivElement>(null);
  const sections = useRef(new Map<HTMLDetailsElement, OpenSection>());
  const previousScope = useRef(scope);
  const previousActorScope = useRef(actorScope);
  const previousLocation = useRef<{ hash: string; pathname: string; search: string } | null>(null);
  const serial = useRef(0);
  const [request, setRequest] = useState<{ id: string; serial: number } | null>(null);
  const [missing, setMissing] = useState(false);
  const register = useCallback((element: HTMLDetailsElement, open: OpenSection) => {
    sections.current.set(element, open);
    return () => { sections.current.delete(element); };
  }, []);
  const context = useMemo(() => ({ register }), [register]);
  const requestId = useCallback((id: string | null) => {
    serial.current += 1;
    setMissing(false);
    setRequest(id ? { id, serial: serial.current } : null);
  }, []);
  const requestHash = useCallback((hash: string) => {
    const id = fragment(hash);
    requestId(id);
    if (hash.length > 1 && id === null) setMissing(true);
  }, [requestId]);

  useEffect(() => {
    const changedScope = previousScope.current !== scope;
    const oldActorScope = previousActorScope.current;
    const changedActor = oldActorScope !== actorScope;
    previousScope.current = scope;
    previousActorScope.current = actorScope;
    const previous = previousLocation.current;
    previousLocation.current = { hash: location.hash, pathname: location.pathname, search: location.search };
    const changedLocation = previous !== null && (previous.pathname !== location.pathname || previous.search !== location.search || previous.hash !== location.hash);
    if (changedActor && oldActorScope) {
      requestId(null);
      return;
    }
    if (changedScope || changedActor) {
      // Keep an initial request (or its cancellation) when only identity finishes loading.
      if (!oldActorScope && actorScope && !changedLocation) return;
      if (actorScope && changedLocation) requestHash(location.hash);
      else requestId(null);
      return;
    }
    if (!previous || previous.hash !== location.hash || previous.pathname !== location.pathname || navigationType === "POP") {
      requestHash(location.hash);
    }
  }, [actorScope, location.hash, location.key, location.pathname, location.search, navigationType, requestHash, requestId, scope]);

  useEffect(() => {
    if (!request || !scope) return;
    let cancelled = false;
    let frame = 0;
    let running = false;
    const cancel = () => {
      cancelled = true;
      serial.current += 1;
      setRequest(null);
    };
    // An old asynchronous target must not move focus after the user continues.
    document.addEventListener("pointerdown", cancel, true);
    document.addEventListener("keydown", cancel, true);
    document.addEventListener("focusin", cancel, true);
    document.addEventListener("wheel", cancel, true);
    const resolve = () => {
      if (!ready || cancelled || running || serial.current !== request.serial) return;
      const target = document.getElementById(request.id);
      if (!(target instanceof HTMLElement) || !root.current?.contains(target)) {
        if (!targetPending?.(request.id)) {
          setMissing(true);
          setRequest(null);
        }
        return;
      }
      running = true;
      let ancestor: HTMLElement | null = target.firstElementChild instanceof HTMLDetailsElement
        ? target.firstElementChild : target;
      while (ancestor && root.current?.contains(ancestor)) {
        if (ancestor instanceof HTMLDetailsElement && !ancestor.open) {
          const open = sections.current.get(ancestor);
          if (open) open();
          else ancestor.open = true;
        }
        ancestor = ancestor.parentElement;
      }
      frame = requestAnimationFrame(() => {
        frame = requestAnimationFrame(() => {
          if (cancelled || serial.current !== request.serial || !target.isConnected) return;
          let parent: HTMLElement | null = target;
          while (parent && root.current?.contains(parent)) {
            if (parent instanceof HTMLDetailsElement && !parent.open) return;
            parent = parent.parentElement;
          }
          setRequest(null);
          focusAndScroll(target);
        });
      });
    };
    const observer = new MutationObserver(resolve);
    if (root.current) observer.observe(root.current, { childList: true, subtree: true });
    resolve();
    return () => {
      cancelled = true;
      cancelAnimationFrame(frame);
      observer.disconnect();
      document.removeEventListener("pointerdown", cancel, true);
      document.removeEventListener("keydown", cancel, true);
      document.removeEventListener("focusin", cancel, true);
      document.removeEventListener("wheel", cancel, true);
    };
  }, [ready, request, scope, targetPending]);

  function onClick(event: MouseEvent<HTMLDivElement>) {
    if (event.defaultPrevented || event.button !== 0 || event.metaKey || event.ctrlKey || event.altKey || event.shiftKey) return;
    const element = event.target instanceof Element ? event.target.closest("a[href]") : null;
    const href = element?.getAttribute("href");
    const id = href ? fragment(href) : null;
    if (!href || !id || element?.getAttribute("target")) return;
    event.preventDefault();
    if (href === location.hash) requestId(id);
    else void navigate({ pathname: location.pathname, search: location.search, hash: href }, { state: location.state });
  }

  return (
    <SectionNavigationContext.Provider value={context}>
      <div ref={root} onClickCapture={onClick}>
        {missing ? <p role="alert">无法找到或读取所选分区，请返回页面目录或重试。</p> : null}
        {children}
      </div>
    </SectionNavigationContext.Provider>
  );
}
