import type { StoryboardAssetRole, StoryboardCharacterReference, StoryboardRow } from "@/types/canvas";
import { assetCategoryLabel } from "@/lib/asset-category";
import { withStoryboardOutputContract } from "@/lib/canvas/canvas-project-domain";
import { normalizeStoryboardAssetBindings } from "@/lib/canvas/canvas-storyboard-assets";
import { storyboardCharacterAppearancePrompt, storyboardStylePrompt } from "@/lib/canvas/canvas-storyboard-context";
import { formatShotOrdinal } from "@/lib/shot-label";
import type { ProjectDetail, ShotRevisionInput } from "@/services/api/projects";
import { ensureShotAssetMentionPrompt } from "./workflow-shot-references";

export type ChapterStoryboardAsset = {
    id: string;
    title: string;
    type: "image" | "video" | "audio" | "character";
    category?: string;
    tags: string[];
    prompt: string;
    characterAssetId?: string;
    characterVersionId?: string;
};

export type ChapterStoryboardCharacter = {
    assetId?: string;
    versionId?: string;
    name: string;
    definition: Record<string, unknown>;
};

export type ProjectShotReplacementInput = {
    title: string;
    description: string;
    durationMs: number;
    revision: ShotRevisionInput;
    assetVersionIds: string[];
};

export type ChapterStoryboardReplaceImpact = {
    shotCount: number;
    revisionCount: number;
    referenceCount: number;
    artifactCount: number;
    candidateCount: number;
};

export function chapterStoryboardCharacters(detail: ProjectDetail, unitId?: string): ChapterStoryboardCharacter[] {
    const confirmed = detail.assets.flatMap((asset): ChapterStoryboardCharacter[] => {
        const card = asset.character;
        if (asset.category !== "character" || !card?.versionId) return [];
        return [{ assetId: asset.id, versionId: card.versionId, name: asset.title, definition: card.definition || {} }];
    });
    const seenNames = new Set(confirmed.map((character) => normalizeCharacterName(character.name)));
    const pending = detail.assetCandidates.flatMap((candidate): ChapterStoryboardCharacter[] => {
        if (candidate.category !== "character" || candidate.status !== "pending_confirmation") return [];
        if (unitId && candidate.unitId && candidate.unitId !== unitId) return [];
        const name = candidate.name.trim();
        const normalizedName = normalizeCharacterName(name);
        if (!normalizedName || seenNames.has(normalizedName)) return [];
        seenNames.add(normalizedName);
        return [{ name, definition: candidateDefinition(candidate.detailsJson) }];
    });
    return [...confirmed, ...pending];
}

export function chapterStoryboardAssets(detail: ProjectDetail): ChapterStoryboardAsset[] {
    return detail.assets.flatMap((asset): ChapterStoryboardAsset[] => {
        const type = storyboardAssetType(asset.mediaType, asset.category);
        if (!type || (type !== "character" && !asset.primaryVersionId)) return [];
        const characterVersionId = asset.character?.versionId;
        if (type === "character" && !characterVersionId) return [];
        return [{
            id: asset.id,
            title: asset.title,
            type,
            category: asset.category || undefined,
            tags: [],
            prompt: assetPrompt(asset.previewText, asset.character?.definition, asset.title),
            ...(type === "character" ? { characterAssetId: asset.id, characterVersionId } : {}),
        }];
    }).slice(0, 60);
}

type AssetKind = "character" | "scene" | "prop" | "other";
type CatalogEntry = { asset: ChapterStoryboardAsset; key: string; kind: AssetKind };

const SCENE_ASSET_CATEGORIES = new Set(["scene", "environment", "location"]);
const ROLE_BY_KIND: Record<AssetKind, StoryboardAssetRole> = { character: "character", scene: "environment", prop: "prop", other: "style" };
// 资产名比对时忽略空白、括号、引号和常见标点：模型常写成「七床病房（夜）」「“林小满”」。
const NAME_NOISE = /[\s·・,，。、；;：:!！?？"'“”‘’「」『』《》〈〉()（）[\]【】<>_—-]/g;
// 文字兜底匹配看的字段：首帧画面最能说明镜头在哪，排在最前；台词只认说话人，且只用来认角色。
const TEXT_FIELDS = ["imageGenerationPrompt", "plotDescription", "performanceBlocking", "videoMotionPrompt", "continuityOut"] as const;

/**
 * 章节分镜任务实际发给模型的提示词。后端不读任务 input 里的 projectStyle / characters / canvasAssets，
 * 项目画风、已确认角色的外观和资产清单都只能写进提示词，各段自带字数上限；画风和角色放在输出契约前，
 * 资产清单带着 assets 字段要求跟在契约后。待确认角色的设定还没定稿，不写外观。
 */
export function chapterStoryboardTaskPrompt(skillPrompt: string, context: { sourceText: string; projectStyle: { title: string; prompt: string }; characters: ChapterStoryboardCharacter[]; assets: ChapterStoryboardAsset[] }) {
    const confirmed = context.characters.filter((character) => character.assetId && character.versionId);
    const projectContext = [skillPrompt, storyboardStylePrompt(context.projectStyle), storyboardCharacterAppearancePrompt(confirmed, context.sourceText)].filter(Boolean).join("\n\n");
    return [withStoryboardOutputContract(projectContext), chapterStoryboardAssetPrompt(context.assets)].filter(Boolean).join("\n\n");
}

/**
 * 生成分镜时交给模型的资产清单：后端不读任务 input 里的 canvasAssets，清单只能写进提示词；
 * 并要求每个镜头在 assets 里写出它画面里出现的资产，bindStoryboardRowAssets 按名称落成绑定。
 */
export function chapterStoryboardAssetPrompt(assets: ChapterStoryboardAsset[]) {
    if (!assets.length) return "";
    return [
        "【项目已确认资产】",
        ...assets.map((asset) => {
            const brief = asset.prompt.trim().startsWith("{") ? "" : asset.prompt.replace(/\s+/g, " ").trim().slice(0, 40);
            return `- ${asset.title}（${assetCategoryLabel(asset.type === "character" ? "character" : asset.category)}）${brief && brief !== asset.title ? `：${brief}` : ""}`;
        }),
        "- rows 每项再加 assets（字符串数组）：本镜画面里出现的上面这些角色、场景和道具，名称原样抄写，清单外的不写，没有就写空数组；角色同时写进 characters。镜头换了场景，assets 里就写新场景。",
    ].join("\n");
}

/**
 * 分镜行落资产绑定，装段才能按场景切场、按绑定带参考：模型在 assets / characters / assetBindings 里给的
 * 资产名或资产 ID 先落到 assetBindings；角色、场景、道具中模型某一类一个都没给时，按镜头文字里出现的资产名
 * 补这一类（场景只补最先出现的那个）。模型给的优先级高于文字匹配；characters 里的名字补上角色资产 ID。
 */
export function bindStoryboardRowAssets(rows: StoryboardRow[], assets: ChapterStoryboardAsset[]): StoryboardRow[] {
    const catalog: CatalogEntry[] = assets.map((asset) => ({ asset, key: normalizeAssetName(asset.title), kind: assetKind(asset) })).filter((entry) => entry.key);
    const resolve = (mention: string) => resolveAssetMention(mention, catalog);
    return rows.map((row) => {
        const given = uniqueEntries(modelAssetMentions(row).flatMap((mention) => resolve(mention) || []));
        const givenKinds = new Set(given.map((entry) => entry.kind));
        const matched = textAssetMatches(row, catalog).filter((entry) => !givenKinds.has(entry.kind));
        const firstScene = matched.find((entry) => entry.kind === "scene");
        const fallback = matched.filter((entry) => entry.kind !== "scene" || entry === firstScene);
        const bindings = [
            ...given.map((entry, index) => ({ nodeId: entry.asset.id, role: ROLE_BY_KIND[entry.kind], priority: Math.max(51, 100 - index) })),
            ...fallback.map((entry, index) => ({ nodeId: entry.asset.id, role: ROLE_BY_KIND[entry.kind], priority: Math.max(1, 50 - index) })),
        ];
        const characters = ((row.characters || []) as unknown[]).flatMap((item): StoryboardCharacterReference[] => {
            const reference = typeof item === "string" ? { characterName: item.trim() } : item && typeof item === "object" ? item as StoryboardCharacterReference : undefined;
            if (!reference?.characterName?.trim() && !reference?.characterAssetId) return [];
            if (reference.characterAssetId) return [reference];
            const entry = resolve(reference.characterName);
            return [entry?.kind === "character" ? { ...reference, characterAssetId: entry.asset.characterAssetId || entry.asset.id, ...(entry.asset.characterVersionId ? { characterVersionId: entry.asset.characterVersionId } : {}) } : reference];
        });
        return { ...row, characters, assetBindings: normalizeStoryboardAssetBindings(bindings) };
    });
}

function assetKind(asset: ChapterStoryboardAsset): AssetKind {
    if (asset.type === "character" || asset.category === "character") return "character";
    if (SCENE_ASSET_CATEGORIES.has(asset.category || "")) return "scene";
    if (asset.category === "prop") return "prop";
    return "other";
}

function normalizeAssetName(value: string) {
    return value.normalize("NFKC").toLocaleLowerCase().replace(NAME_NOISE, "");
}

/** 模型给出的资产：新契约的 assets、characters 里的角色名，以及偶尔直接写出的 assetBindings。 */
function modelAssetMentions(row: StoryboardRow) {
    const raw = (row as StoryboardRow & { assets?: unknown }).assets;
    const listed = Array.isArray(raw) ? raw : typeof raw === "string" ? raw.split(/[、，,;；\n]/) : [];
    const text = (value: unknown): string[] => {
        if (typeof value === "string") return [value];
        if (!value || typeof value !== "object") return [];
        const record = value as Record<string, unknown>;
        return ["id", "assetId", "characterAssetId", "name", "title", "characterName"].flatMap((key) => typeof record[key] === "string" ? [record[key] as string] : []).slice(0, 1);
    };
    return [...listed, ...((row.characters || []) as unknown[]), ...row.assetBindings.map((binding) => binding.nodeId)].flatMap(text).map((value) => value.trim()).filter(Boolean);
}

/** 资产 ID 精确匹配，其次名称（忽略标点）相同；都不中时取名称互相包含且唯一的那个（「七床病房（夜）」→「七床病房」）。 */
function resolveAssetMention(mention: string, catalog: CatalogEntry[]) {
    const byId = catalog.find((entry) => entry.asset.id === mention || entry.asset.characterAssetId === mention);
    if (byId) return byId;
    const key = normalizeAssetName(mention);
    if (!key) return undefined;
    const exact = catalog.find((entry) => entry.key === key);
    if (exact) return exact;
    const inside = catalog.filter((entry) => entry.key.length >= 2 && key.includes(entry.key));
    const longest = Math.max(0, ...inside.map((entry) => entry.key.length));
    const best = inside.filter((entry) => entry.key.length === longest);
    if (best.length === 1) return best[0];
    const around = key.length >= 2 ? catalog.filter((entry) => entry.key.includes(key)) : [];
    return around.length === 1 ? around[0] : undefined;
}

/** 镜头文字里出现的资产名，按出现先后（字段顺序见 TEXT_FIELDS）；长名先匹配并遮住，「病房走廊」不会再算成「病房」。 */
function textAssetMatches(row: StoryboardRow, catalog: CatalogEntry[]) {
    const candidates = catalog.filter((entry) => entry.kind !== "other" && entry.key.length >= 2).sort((left, right) => right.key.length - left.key.length);
    const speakers = row.dialogue.split(/\n+/).flatMap((line) => {
        const match = /^\s*([^：:]{1,16})[：:]/.exec(line);
        return match && !/画外|旁白|OS|VO/i.test(match[1]) ? [match[1]] : [];
    }).join("\n");
    const fields: Array<{ text: string; characterOnly: boolean }> = [...TEXT_FIELDS.map((field) => ({ text: row[field], characterOnly: false })), { text: speakers, characterOnly: true }];
    const rank = new Map<CatalogEntry, number>();
    fields.forEach(({ text, characterOnly }, fieldIndex) => {
        let normalized = normalizeAssetName(text || "");
        for (const entry of candidates) {
            if (characterOnly && entry.kind !== "character") continue;
            const index = normalized.indexOf(entry.key);
            if (index < 0) continue;
            const position = fieldIndex * 100_000 + index;
            if (!rank.has(entry) || position < (rank.get(entry) as number)) rank.set(entry, position);
            normalized = normalized.split(entry.key).join("\u0000".repeat(entry.key.length));
        }
    });
    return Array.from(rank.entries()).sort((left, right) => left[1] - right[1]).map(([entry]) => entry);
}

function uniqueEntries(entries: CatalogEntry[]) {
    return Array.from(new Set(entries));
}

export function storyboardRowsToProjectShots(rows: StoryboardRow[], detail?: ProjectDetail): ProjectShotReplacementInput[] {
    const versionByAssetId = new Map((detail?.assets || []).flatMap((asset) => asset.primaryVersionId ? [[asset.id, asset.primaryVersionId] as const] : []));
    const mentionReferenceByAssetId = new Map((detail?.assets || []).map((asset) => [asset.id, {
        assetId: asset.id,
        kind: asset.category === "character" ? "character" as const : "image" as const,
        label: asset.title,
        title: asset.title,
    }]));
    return rows.map((row, index) => {
        const shotNumber = index + 1;
        const description = row.plotDescription.trim() || `镜头 ${shotNumber}`;
        const durationMs = Math.max(1000, Math.round((Number(row.durationSeconds) || 1) * 1000));
        const mentionReferences = storyboardRowAssetIds(row).flatMap((assetId) => {
            const reference = mentionReferenceByAssetId.get(assetId);
            return reference ? [reference] : [];
        });
        return {
            title: formatShotOrdinal(index),
            description,
            durationMs,
            assetVersionIds: storyboardRowAssetVersionIds(row, versionByAssetId),
            revision: {
                plotDescription: description,
                action: joinStoryboardAction(row),
                dialogue: row.dialogue.trim(),
                shotSize: row.shotSize.trim(),
                cameraAngle: row.camera.trim(),
                cameraMovement: row.motion.trim(),
                durationMs,
                imagePrompt: row.imageGenerationPrompt.trim(),
                videoPrompt: ensureShotAssetMentionPrompt(row.videoMotionPrompt, mentionReferences),
                negativePrompt: row.negativePrompt.trim(),
                continuityNotes: row.continuityOut.trim(),
                actionBeats: row.timeBeats.trim() ? [{ description: row.timeBeats.trim() }] : [],
            },
        };
    });
}

export function chapterStoryboardReplaceImpact(detail: ProjectDetail, unitId: string): ChapterStoryboardReplaceImpact {
    const shots = detail.shots.filter((shot) => shot.unitId === unitId);
    const shotIds = new Set(shots.map((shot) => shot.id));
    return {
        shotCount: shots.length,
        revisionCount: detail.shotRevisions.filter((revision) => shotIds.has(revision.shotId)).length,
        referenceCount: detail.shotReferences.filter((reference) => shotIds.has(reference.shotId)).length,
        artifactCount: detail.shotArtifacts.filter((artifact) => artifact.unitId === unitId).length,
        candidateCount: detail.assetCandidates.filter((candidate) => Boolean(candidate.shotId && shotIds.has(candidate.shotId))).length,
    };
}

function storyboardRowAssetVersionIds(row: StoryboardRow, versionByAssetId: Map<string, string>) {
    return storyboardRowAssetIds(row).flatMap((assetId) => {
        const versionId = versionByAssetId.get(assetId);
        return versionId ? [versionId] : [];
    }).slice(0, 6);
}

function storyboardRowAssetIds(row: StoryboardRow) {
    return Array.from(new Set([
        ...row.assetBindings.map((binding) => binding.nodeId),
        ...row.characters.flatMap((character) => character.characterAssetId ? [character.characterAssetId] : []),
    ])).slice(0, 6);
}

function storyboardAssetType(mediaType: string, category: string): ChapterStoryboardAsset["type"] | null {
    if (category === "character") return "character";
    if (mediaType === "image" || mediaType === "video" || mediaType === "audio") return mediaType;
    return null;
}

function assetPrompt(previewText: string | undefined, definition: Record<string, unknown> | undefined, title: string) {
    const preview = previewText?.replace(/\s+/g, " ").trim();
    if (preview) return preview.slice(0, 600);
    if (definition && Object.keys(definition).length) return JSON.stringify(definition).slice(0, 600);
    return title.trim();
}

function candidateDefinition(value: string): Record<string, unknown> {
    try {
        const parsed: unknown = JSON.parse(value || "{}");
        return parsed && typeof parsed === "object" && !Array.isArray(parsed) ? parsed as Record<string, unknown> : {};
    } catch {
        return {};
    }
}

function normalizeCharacterName(value: string) {
    return value.trim().toLocaleLowerCase();
}

function joinStoryboardAction(row: StoryboardRow) {
    return [
        labeledText("叙事意图", row.narrativeIntent),
        labeledText("表演调度", row.performanceBlocking),
        labeledText("时间节拍", row.timeBeats),
        labeledText("情绪", row.emotion),
        labeledText("光线与氛围", row.lightingAndAtmosphere),
        labeledText("声音", row.audioEffects),
    ].filter(Boolean).join("\n");
}

function labeledText(label: string, value: string) {
    const text = value.trim();
    return text ? `${label}：${text}` : "";
}
