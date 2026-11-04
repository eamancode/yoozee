import { expect, test } from "bun:test";
import { characterDefinition, characterFields, type CharacterForm } from "../../src/pages/projects/workbench/detail/project-character-form";

function form(overrides: Partial<CharacterForm> = {}): CharacterForm {
    return { name: "林深", ...Object.fromEntries(characterFields.map(([key]) => [key, ""])), ...overrides } as CharacterForm;
}

test("editing a main card preserves attachment identity, evidence, states and references", () => {
    const current = {
        appearance: "原外貌",
        aliases: ["林总"],
        identityAppearance: "眉眼与脸型",
        extractionDesign: { evidence: [{ source: "第一场", type: "原文", text: "五年前" }], states: [{ scene: 1, state: "学生" }], props: ["旧保温杯"] },
    };
    const original = structuredClone(current);
    const saved = characterDefinition(form({ appearance: " 新外貌 ", aliases: "林总， 小林" }), current);
    expect(saved.appearance).toBe("新外貌");
    expect(saved.aliases).toEqual(["林总", "小林"]);
    expect(saved.identityAppearance).toBe(current.identityAppearance);
    expect(saved.extractionDesign).toEqual(current.extractionDesign);
    expect(current).toEqual(original);
});

test("clearing editable fields is explicit and creating a card has no inherited metadata", () => {
    const cleared = characterDefinition(form(), { clothing: "西装", aliases: ["林总"] });
    expect(cleared.clothing).toBe("");
    expect(cleared.aliases).toEqual([]);
    const created = characterDefinition(form({ clothing: " 衬衫 " }));
    expect(created.clothing).toBe("衬衫");
    expect(created).not.toHaveProperty("extractionDesign");
    expect(created).not.toHaveProperty("name");
});
