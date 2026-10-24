import { expect, test } from "bun:test";

import { defaultFeatureAvailability } from "../src/stores/use-user-store";

test("skill details stay visible until an admin turns skill display off", () => {
    expect(defaultFeatureAvailability.skillContentVisible).toBe(true);
});

test("hidden skill display removes the detail and agent skill entry points", async () => {
    const [skillsPage, agentPanel, agentSettings] = await Promise.all([
        Bun.file(new URL("../src/pages/skills/index.tsx", import.meta.url)).text(),
        Bun.file(new URL("../src/components/canvas/canvas-cloud-agent-panel.tsx", import.meta.url)).text(),
        Bun.file(new URL("../src/components/canvas/canvas-cloud-agent-settings.tsx", import.meta.url)).text(),
    ]);

    expect(skillsPage).toContain("onOpen={skillContentVisible ? () => void openSkill(skill) : undefined}");
    expect(skillsPage).toContain("<SkillDetailModal skill={skillContentVisible ? activeSkill : null}");
    expect(agentPanel).toContain("onSkillsOpenChange={skillContentVisible ? setSkillsOpen : undefined}");
    expect(agentPanel).toContain("slashSkills={skillContentVisible ? installedSkills : []}");
    expect(agentPanel).toContain("open={skillsOpen && skillContentVisible}");
    expect(agentSettings).toContain('sectionState === "skills" && !props.skillsVisible ? "home" : sectionState');
});
