import type { CanvasNodeData } from "@/types/canvas";

export type StoryboardGenerationContext = {
    characters: Array<{
        assetId: string;
        versionId: string;
        name: string;
        definition: Record<string, unknown>;
    }>;
};

// 分镜的两条入口共用角色资产校验，避免角色版本在某条旁路里被遗漏。
export function resolveStoryboardGenerationContext(nodes: CanvasNodeData[]): StoryboardGenerationContext {
    // `workflowKind=character` is also used by standalone character-design
    // image workflows. Only nodes linked to a project character asset are
    // storyboard character cards and therefore participate in this check.
    const characterNodes = nodes.filter((node) => node.metadata?.workflowKind === "character" && node.metadata?.characterAssetId?.trim());
    const invalidCharacter = characterNodes.find((node) => !node.metadata?.characterAssetId?.trim() || !node.metadata?.characterVersionId?.trim() || !(node.metadata?.characterName || node.title).trim());
    if (invalidCharacter) throw new Error(`角色卡“${invalidCharacter.metadata?.characterName || invalidCharacter.title || "未命名角色"}”版本未同步，请刷新角色资产后再生成分镜`);

    return {
        characters: characterNodes.map((node) => ({
            assetId: node.metadata!.characterAssetId!.trim(),
            versionId: node.metadata!.characterVersionId!.trim(),
            name: (node.metadata?.characterName || node.title).trim(),
            definition: node.metadata?.characterDefinition || { prompt: node.metadata?.characterPrompt || "" },
        })),
    };
}

// Expose the same generation prerequisites to the Agent before it starts a workflow.
export function inspectStoryboardReadiness(nodes: CanvasNodeData[]) {
    let generationContext: StoryboardGenerationContext | undefined;
    let blockingReason: string | undefined;
    try { generationContext = resolveStoryboardGenerationContext(nodes); }
    catch (error) { blockingReason = error instanceof Error ? error.message : "分镜上下文未就绪"; }
    const styleNode = nodes.find((node) => node.metadata?.workflowKind === "styleboard");
    const styleReady = Boolean(styleNode?.metadata?.stylePresetId?.trim() && String(styleNode.metadata.content || styleNode.metadata.prompt || "").trim());
    const storyboards = nodes.filter((node) => node.type === "script").map((node) => {
        const rows = node.metadata?.storyboard?.rows || [];
        const incompleteRowIds = rows.filter((row) => !Number.isFinite(row.durationSeconds) || row.durationSeconds <= 0 || !row.videoMotionPrompt?.trim()).map((row) => row.id);
        return { nodeId: node.id, title: node.title, rowCount: rows.length, populated: rows.length > 0 && incompleteRowIds.length === 0, incompleteRowIds, totalDurationSeconds: rows.reduce((total, row) => total + (Number.isFinite(row.durationSeconds) && row.durationSeconds > 0 ? row.durationSeconds : 0), 0) };
    });
    return {
        canGenerateStoryboard: Boolean(generationContext), blockingReason,
        style: { ready: styleReady, nodeId: styleNode?.id, presetId: styleNode?.metadata?.stylePresetId, title: styleNode?.title },
        storyboards,
        nextActions: [
            ...(!styleReady ? ["先 canvas_list_styles 读取真实画风，结合已确认风格调用 canvas_apply_style；该工具会复用或创建画风节点。仅在缺少关键风格取舍时询问用户。"] : []),
            ...(storyboards.some((item) => !item.populated) ? ["先 canvas_read_storyboard 读取真实行 ID，再 canvas_edit_storyboard 更新空行并按需新增镜头；正文或标题不代表已填充分镜表。"] : []),
        ],
    };
}

// 分镜任务的项目画风和角色外观只能写进提示词：后端 canvasGenerationInput 不读任务 input 里的 projectStyle / characters。
// 两段都限长，正文和技能上下文的预算优先。
const STYLE_PROMPT_CHARS = 1200;
const CHARACTER_PROMPT_CHARS = 1600;
const CHARACTER_LINE_CHARS = 220;
const CHARACTER_FIELD_CHARS = 80;
// 画风小节按对写分镜画面的用处排序，超预算时从后往前舍弃：项目定位与风格组合重复，资产一致性是通用约束。
const STORYBOARD_STYLE_SECTIONS = ["风格组合", "视觉媒介", "角色设计系统", "项目色彩与光影", "服饰、材质与场景", "全局禁用", "影像与动态基线", "叙事气质", "题材世界观", "使用边界", "资产一致性", "项目定位"];
const CHARACTER_VISUAL_FIELDS = [["appearance", "外貌"], ["clothing", "服装"], ["physique", "体型"], ["props", "随身道具"], ["consistencyPrompt", "一致性"]] as const;

/** 项目画风按小节取到预算为止、按原顺序输出；自由书写的画风没有【】小节，按行截取。 */
export function storyboardStylePrompt(style?: { title: string; prompt: string }) {
    const lines = (style?.prompt || "").split("\n").map(compactText).filter(Boolean);
    if (!style || !lines.length) return "";
    const rank = (line: string) => {
        const index = STORYBOARD_STYLE_SECTIONS.indexOf(/^【([^】]+)】/.exec(line)?.[1] || "");
        return index < 0 ? STORYBOARD_STYLE_SECTIONS.length : index;
    };
    const kept = new Map<number, string>();
    let remaining = STYLE_PROMPT_CHARS;
    for (const { line, index } of lines.map((line, index) => ({ line, index })).sort((left, right) => rank(left.line) - rank(right.line) || left.index - right.index)) {
        if (line.length <= remaining) {
            kept.set(index, line);
            remaining -= line.length;
        } else if (remaining >= 60) {
            kept.set(index, `${line.slice(0, remaining - 1)}…`);
            break;
        }
    }
    return [
        `【项目画风：${style.title.trim() || "未命名画风"}】分镜里的画面、光影、服饰和场景描述都按这个画风写，不要写成其他媒介；这是项目级基线，不要整段抄进每个镜头。`,
        ...Array.from(kept.entries()).sort((left, right) => left[0] - right[0]).map(([, line]) => line),
    ].join("\n");
}

/** 角色外观只取画面相关字段；本章正文里先出现的角色排在前面，超预算的角色不写（资产清单里仍有名字）。 */
export function storyboardCharacterAppearancePrompt(characters: Array<{ name: string; definition: Record<string, unknown> }>, sourceText = "") {
    const firstMention = (character: { name: string; definition: Record<string, unknown> }) => {
        const aliases = Array.isArray(character.definition.aliases) ? character.definition.aliases : [];
        const positions = [character.name, ...aliases].flatMap((name) => {
            const index = typeof name === "string" && name.trim() ? sourceText.indexOf(name.trim()) : -1;
            return index < 0 ? [] : [index];
        });
        return positions.length ? Math.min(...positions) : Number.POSITIVE_INFINITY;
    };
    const ordered = characters
        .map((character, index) => ({ line: characterAppearanceLine(character), position: firstMention(character), index }))
        .filter((item) => item.line)
        .sort((left, right) => left.position - right.position || left.index - right.index);
    const lines: string[] = [];
    let remaining = CHARACTER_PROMPT_CHARS;
    for (const { line } of ordered) {
        if (line.length > remaining) break;
        lines.push(line);
        remaining -= line.length;
    }
    if (!lines.length) return "";
    return ["【已确认角色外观】角色出镜时，imageGenerationPrompt 写出本镜看得见的外貌、发型、服装和标志物，与下面的设定一致，不要自行改换；剧情明确换装或受伤时才改。", ...lines].join("\n");
}

function characterAppearanceLine(character: { name: string; definition: Record<string, unknown> }) {
    const name = character.name.trim();
    const parts = CHARACTER_VISUAL_FIELDS.flatMap(([key, label]) => {
        const text = truncateText(compactText(character.definition[key]), CHARACTER_FIELD_CHARS);
        return text ? [`${label}：${text}`] : [];
    });
    // 画布角色卡只有一段角色提示词时，definition 是 { prompt }。
    if (!parts.length) {
        const prompt = compactText(character.definition.prompt);
        if (prompt) parts.push(prompt);
    }
    if (!name || !parts.length) return "";
    return truncateText(`- ${name}｜${parts.join("；")}`, CHARACTER_LINE_CHARS);
}

function compactText(value: unknown) {
    return typeof value === "string" ? value.replace(/\s+/g, " ").trim() : "";
}

function truncateText(value: string, maxChars: number) {
    return value.length > maxChars ? `${value.slice(0, maxChars - 1)}…` : value;
}
