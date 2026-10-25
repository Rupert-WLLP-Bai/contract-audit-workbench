import { paths } from "./paths";

describe("api paths", () => {
  it("matches the assumed contract and encodes ids", () => {
    expect(paths.session).toBe("/api/session");
    expect(paths.cases).toBe("/api/cases");
    expect(paths.case("c1")).toBe("/api/cases/c1");
    expect(paths.documents("c1")).toBe("/api/cases/c1/documents");
    expect(paths.reparse("c1", "d1")).toBe("/api/cases/c1/documents/d1/parse");
    expect(paths.reviews("c1")).toBe("/api/cases/c1/reviews");
    expect(paths.review("c1", "r1")).toBe("/api/cases/c1/reviews/r1");
    expect(paths.retryReview("c1", "r1")).toBe("/api/cases/c1/reviews/r1/retry");
    expect(paths.decision("a/b", "r1", "f1")).toBe("/api/cases/a%2Fb/reviews/r1/findings/f1/decisions");
    expect(paths.reviewEvents("c1", "r1")).toBe("/api/cases/c1/reviews/r1/events");
    expect(paths.settings).toBe("/api/settings");
  });
});
