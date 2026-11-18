import { modelCapabilityConfigFor } from "@/lib/model-capabilities";
import { runBackendGenerationTask } from "@/services/api/generation-task";
import type { SceneFacts, SceneSpace } from "@/services/api/project-drama";
import { resourceStorageKey } from "@/services/api/resources";
import type { AiConfig } from "@/stores/use-config-store";

const readScenePrompt =
    "查看这张短剧场景参考图，只记录图上确实能看到的固定空间。提取室内外、主体布局、建筑或出入口与固定陈设锚点。忽略分镜拼图的边框、标签、文字、光照、演员、临时道具和镜头裁切。画外、遮挡或无法确认的要点归入 unknown，不推测为不存在。输出一个 JSON 对象，字段为 inOut（内、外、内外或空字符串）、layout（简短中文）、anchors（中文字符串数组）、unknown（中文字符串数组）。只输出 JSON。";

export async function extractSceneFacts(projectId: string, space: SceneSpace, config: AiConfig, textModel: string): Promise<SceneFacts> {
    if (!space.resourceId) throw new Error("请先选择有可用图片的场景");
    if (!textModel || !modelCapabilityConfigFor(config, textModel).text?.references.maxImages) {
        throw new Error("当前文字模型无法读取图片，请选择支持图片输入的文字模型，或手工填写锚点");
    }
    const result = await runBackendGenerationTask({
        projectId,
        mode: "text",
        config: { ...config, model: textModel, textModel },
        referenceImages: [{ id: space.resourceId, name: space.label || "场景参考图", type: "image/*", storageKey: resourceStorageKey(space.resourceId), dataUrl: "" }],
        prompt: readScenePrompt,
        metadata: { source: "short-drama-scene-facts", sceneResourceId: space.resourceId },
    });
    return parseSceneFacts(result.text || "");
}

export function parseSceneFacts(text: string): SceneFacts {
    let value: unknown;
    try {
        value = JSON.parse(text.trim().replace(/^```(?:json)?\s*|\s*```$/gi, ""));
    } catch {
        throw new Error("识图结果格式无效，可重新提取或手工填写");
    }
    if (!value || typeof value !== "object" || Array.isArray(value)) throw new Error("识图结果格式无效");
    const { inOut, layout, anchors, unknown } = value as Record<string, unknown>;
    const list = (raw: unknown) =>
        Array.isArray(raw)
            ? [
                  ...new Set(
                      raw
                          .filter((item): item is string => typeof item === "string")
                          .map((item) => item.trim().replace(/[\r\n]+/g, " "))
                          .filter(Boolean),
                  ),
              ]
                  .slice(0, 30)
                  .map((item) => Array.from(item).slice(0, 60).join(""))
            : [];
    const facts = {
        inOut: typeof inOut === "string" && ["内", "外", "内外"].includes(inOut) ? inOut : "",
        layout: typeof layout === "string" ? Array.from(layout.trim()).slice(0, 1500).join("") : "",
        anchors: list(anchors),
        unknown: list(unknown),
    };
    if (!facts.inOut && !facts.layout && !facts.anchors.length && !facts.unknown.length) throw new Error("图片中没有提取到可用的空间信息");
    return facts;
}
