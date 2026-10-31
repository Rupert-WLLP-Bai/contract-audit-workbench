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
    expect(paths.reviewDiagnostics("a/b", "r1", "attempt/1")).toBe(
      "/api/cases/a%2Fb/reviews/r1/diagnostics?attempt_id=attempt%2F1",
    );
    expect(paths.retryReview("c1", "r1")).toBe(
      "/api/cases/c1/reviews/r1/retry",
    );
    expect(paths.decision("a/b", "r1", "f1")).toBe(
      "/api/cases/a%2Fb/reviews/r1/findings/f1/decisions",
    );
    expect(paths.reviewEvents("c1", "r1")).toBe(
      "/api/cases/c1/reviews/r1/events",
    );
    expect(paths.subjects()).toBe("/api/subjects");
    expect(paths.subjects("case/a", "甲方" )).toBe(
      "/api/subjects?case_id=case%2Fa&q=%E7%94%B2%E6%96%B9",
    );
    expect(paths.subjects(undefined, "甲方", "cursor/1", 50)).toBe(
      "/api/subjects?q=%E7%94%B2%E6%96%B9&cursor=cursor%2F1&limit=50",
    );
    expect(paths.subject("s/1")).toBe("/api/subjects/s%2F1");
    expect(paths.subjectHistory("s/1")).toBe("/api/subjects/s%2F1/history");
    expect(paths.subjectCaseLinks("s/1")).toBe("/api/subjects/s%2F1/case-links");
    expect(paths.subjectCaseLinks("s/1", "next/1", 50)).toBe("/api/subjects/s%2F1/case-links?cursor=next%2F1&limit=50");
    expect(paths.caseSubjects("c/1")).toBe("/api/cases/c%2F1/subjects");
    expect(paths.caseLegalApplicabilityFactHistory("c/1", "buyer_kind", "next/1", 20)).toBe(
      "/api/cases/c%2F1/legal-applicability-facts/buyer_kind/history?cursor=next%2F1&limit=20",
    );
    expect(paths.caseSubjectsBindExisting("c/1")).toBe("/api/cases/c%2F1/subjects/bind-existing");
    expect(paths.caseSubjectSourceSnapshots("c/1")).toBe("/api/cases/c%2F1/subject-source-snapshots");
    expect(paths.caseSubjectSourceSnapshots("c/1", "next/1", 100)).toBe("/api/cases/c%2F1/subject-source-snapshots?cursor=next%2F1&limit=100");
    expect(paths.caseSubjectSourceSnapshot("c/1", "snap/1")).toBe("/api/cases/c%2F1/subject-source-snapshots/snap%2F1");
    expect(paths.caseSubjectSourceSnapshotVerify("c/1", "snap/1")).toBe(
      "/api/cases/c%2F1/subject-source-snapshots/snap%2F1/verify",
    );
    expect(paths.materialParsedDocuments("c/1", "mat/1", 2)).toBe("/api/cases/c%2F1/materials/mat%2F1/versions/2/parsed-documents");
    expect(paths.materialParsedText("c/1", "mat/1", 2, "parse/2", 12)).toBe("/api/cases/c%2F1/materials/mat%2F1/versions/2/parsed-documents/parse%2F2?offset=12&limit=32768");
    expect(paths.caseSubjectChanges("c/1")).toBe("/api/cases/c%2F1/subject-changes");
    expect(paths.caseSubjectChange("c/1", "r/1")).toBe(
      "/api/cases/c%2F1/subject-changes/r%2F1",
    );
    expect(paths.caseSubjectChangeImpact("c/1", "r/1")).toBe(
      "/api/cases/c%2F1/subject-changes/r%2F1/impact",
    );
    expect(paths.caseSubjectChangeDecisions("c/1", "r/1")).toBe(
      "/api/cases/c%2F1/subject-changes/r%2F1/decisions",
    );
    expect(paths.settings).toBe("/api/settings");
  });
});
