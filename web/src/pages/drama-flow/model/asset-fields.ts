import type { ProjectAsset } from "@/services/api/projects";

const text = (value: unknown) => (typeof value === "string" ? value.trim() : Array.isArray(value) ? value.filter((item) => typeof item === "string").join("、") : "");

/** 一条结构化原文依据（资产一体化提取写进 extractionDesign.evidence：kind / location / text）。 */
type EvidenceItem = { kind?: unknown; location?: unknown; text?: unknown };

/** 原文依据：旧资产在根上写 evidence / sourceQuote（字符串或字符串数组）；资产一体化提取写在 extractionDesign.evidence（对象数组）。按「kind · location：text」一条一行。 */
export function evidenceText(def: Record<string, unknown>) {
    const design = (def.extractionDesign && typeof def.extractionDesign === "object" ? def.extractionDesign : {}) as Record<string, unknown>;
    for (const value of [def.evidence, def.sourceQuote, design.evidence]) {
        if (typeof value === "string" && value.trim()) return value.trim();
        if (!Array.isArray(value)) continue;
        const lines = value
            .map((item) => {
                if (typeof item === "string") return item.trim();
                if (!item || typeof item !== "object") return "";
                const { kind, location, text: body } = item as EvidenceItem;
                const head = [kind, location]
                    .filter((part) => typeof part === "string" && part.trim())
                    .map((part) => (part as string).trim())
                    .join(" · ");
                const content = typeof body === "string" ? body.trim() : "";
                return content ? (head ? `${head}：${content}` : content) : "";
            })
            .filter(Boolean);
        if (lines.length) return lines.join("\n");
    }
    return "";
}

/** 资产的设定字段：角色看 character.definition，场景道具看 visual.definition；两边字段名不同，统一成几行。 */
export function assetFields(asset: ProjectAsset): Array<{ label: string; value: string }> {
    const def = (asset.character?.definition || asset.visual?.definition || {}) as Record<string, unknown>;
    const rows: Array<[string, unknown]> = asset.character
        ? [
              ["剧情定位", def.role],
              ["外观设定", def.appearance || def.consistencyPrompt],
              ["性格", def.personality],
              ["别名与称呼", def.aliases],
              ["原文依据", evidenceText(def)],
              ["出图提示词", def.generationPrompt || def.multiViewPrompt || def.prompt || def.promptEn],
          ]
        : [
              ["描述", def.description],
              ["原文依据", evidenceText(def)],
              ["出图提示词", def.generationPrompt || def.prompt],
          ];
    return rows.map(([label, value]) => ({ label, value: text(value) }));
}
