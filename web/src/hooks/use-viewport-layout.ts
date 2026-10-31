import { useEffect, useState } from "react";
import { reviewLayout, type ReviewLayout } from "../domain/review";

export function useReviewLayout(): ReviewLayout {
  const [layout, setLayout] = useState<ReviewLayout>(() =>
    typeof window === "undefined" ? "switch" : reviewLayout(window.innerWidth),
  );

  useEffect(() => {
    const media = window.matchMedia("(min-width: 1100px)");
    const apply = () => setLayout(media.matches ? "split" : "switch");
    apply();
    media.addEventListener("change", apply);
    return () => media.removeEventListener("change", apply);
  }, []);

  return layout;
}
