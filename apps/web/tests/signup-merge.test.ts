import { describe, expect, it } from "vitest";

import { draftToForm, mergeDraft } from "@/lib/signup-types";

const empty = draftToForm(null);

describe("wizard draft merge", () => {
  it("keeps earlier answers when the account step posts only its own fields", () => {
    const saved = {
      ...empty,
      examPathwayKey: "waec",
      examPathwayId: "pathway_waec",
      subjectIds: ["subj_math", "subj_eng"],
      targetDate: "2026-05-01",
      dailyMinutes: 45,
    };

    const merged = mergeDraft(saved, { displayName: "Amara", email: "amara@example.com" }, false);

    expect(merged.examPathwayKey).toBe("waec");
    expect(merged.subjectIds).toEqual(["subj_math", "subj_eng"]);
    expect(merged.dailyMinutes).toBe(45);
    expect(merged.displayName).toBe("Amara");
  });

  it("never lets a blank field erase a saved answer", () => {
    const merged = mergeDraft({ ...empty, email: "amara@example.com" }, { email: "" }, false);
    expect(merged.email).toBe("amara@example.com");
  });

  it("treats a rendered subject list as authoritative, so unticking everything sticks", () => {
    const merged = mergeDraft({ ...empty, subjectIds: ["subj_math"] }, { subjectIds: [] }, true);
    expect(merged.subjectIds).toEqual([]);
  });

  it("ignores a subject list that was not part of the posted step", () => {
    const merged = mergeDraft({ ...empty, subjectIds: ["subj_math"] }, { subjectIds: [] }, false);
    expect(merged.subjectIds).toEqual(["subj_math"]);
  });
});
