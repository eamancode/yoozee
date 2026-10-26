import { describe, expect, test } from "bun:test";

import { chapterStoryboardReplaceImpact } from "@/pages/projects/detail/chapter-storyboard-production";
import type { ProjectDetail } from "@/services/api/projects";

describe("章节分镜替换", () => {
    test("替换确认统计该章会被删除的关联数据", () => {
        expect(chapterStoryboardReplaceImpact(projectDetail(), "chapter-1")).toEqual({
            shotCount: 1,
            revisionCount: 1,
            referenceCount: 1,
            artifactCount: 1,
            candidateCount: 1,
        });
    });
});

function projectDetail(): ProjectDetail {
    return {
        project: { id: "project-1", userId: "user-1", name: "雨夜", type: "short_drama", aspectRatio: "16:9", sourceType: "novel", description: "", stylePresetId: "urban-live-action", status: "active", revision: 1, createdAt: "", updatedAt: "" },
        units: [], canvases: [], canvasUnitLinks: [], assetFolders: [], workflows: [],
        assets: [
            { id: "character-1", title: "林默", mediaType: "image", category: "character", status: "ready", primaryVersionId: "asset-version-1", versionCount: 1, usages: [], position: 0, updatedAt: "", character: { versionId: "character-version-1", version: 1, definition: { appearance: "黑色风衣" }, representations: [], visualStatus: "ready", voiceStatus: "missing" } },
            { id: "prop-1", title: "旧信封", mediaType: "image", category: "prop", status: "ready", primaryVersionId: "asset-version-2", versionCount: 1, usages: [], position: 1, previewText: "泛黄的旧信封", updatedAt: "" },
            { id: "draft-prop", title: "未完成道具", mediaType: "image", category: "prop", status: "draft", versionCount: 0, usages: [], position: 2, updatedAt: "" },
        ],
        shots: [{ id: "shot-1", projectId: "project-1", unitId: "chapter-1", currentRevisionId: "revision-1", title: "SC.01", description: "旧分镜", position: 0, durationMs: 3000, status: "draft", createdAt: "", updatedAt: "" }],
        shotRevisions: [{ id: "revision-1", shotId: "shot-1", version: 1, plotDescription: "旧分镜", action: "", dialogue: "", shotSize: "", cameraAngle: "", cameraMovement: "", durationMs: 3000, imagePrompt: "", videoPrompt: "", negativePrompt: "", continuityNotes: "", actionBeatsJson: "[]", createdAt: "" }],
        shotArtifacts: [{ id: "artifact-1", projectId: "project-1", unitId: "chapter-1", shotId: "shot-1", type: "storyboard", version: 1, status: "ready", selected: true, metadataJson: "{}", createdAt: "", updatedAt: "" }],
        shotReferences: [{ id: "reference-1", shotId: "shot-1", assetVersionId: "asset-version-1", role: "reference", status: "linked", createdAt: "" }],
        assetCandidates: [
            { id: "candidate-1", projectId: "project-1", unitId: "chapter-1", shotId: "shot-1", name: "雨伞", category: "prop", status: "pending_confirmation", detailsJson: "{}", createdAt: "", updatedAt: "" },
            { id: "candidate-character-1", projectId: "project-1", unitId: "chapter-1", name: "张天昊", category: "character", status: "pending_confirmation", detailsJson: JSON.stringify({ role: "主角" }), createdAt: "", updatedAt: "" },
            { id: "candidate-character-other", projectId: "project-1", unitId: "chapter-2", name: "另一章角色", category: "character", status: "pending_confirmation", detailsJson: "{}", createdAt: "", updatedAt: "" },
        ],
    };
}
