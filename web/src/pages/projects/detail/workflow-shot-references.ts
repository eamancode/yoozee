import type { CanvasResourceReference } from "@/lib/canvas/canvas-resource-references";
import { assetCategoryLabel, normalizeAssetCategory } from "@/lib/asset-category";
import { resourceFileUrl, resourceIdFromStorageKey, resourceStorageKey } from "@/services/api/resources";
import type { CharacterRepresentation, ProjectAsset, ProjectDetail, ShotAssetReference } from "@/services/api/projects";
import type { AssetCategory } from "@/stores/use-asset-store";
import type { ReferenceImage } from "@/types/image";
import type { ReferenceAudio } from "@/types/media";

export type ShotAssetReferenceContext = {
    mentionReferences: CanvasResourceReference[];
    referenceImages: ReferenceImage[];
    referenceAudios: ReferenceAudio[];
    assetReferences: ShotPromptAssetReference[];
    resolvedCharacterVersions: Array<{ assetId: string; versionId: string }>;
};

export type ShotPromptAssetReference = {
    assetId: string;
    title: string;
    category: string;
    imageIndex: number;
    audioIndex?: number;
    voiceDescription?: string;
    dialogue?: string;
};

export function buildShotAssetReferenceContext(detail: ProjectDetail, shotId: string): ShotAssetReferenceContext {
    const assetByVersionId = new Map(detail.assets.filter((asset) => asset.primaryVersionId).map((asset) => [asset.primaryVersionId as string, asset]));
    const seenAssetIds = new Set<string>();
    const entries = (detail.shotReferences || []).flatMap((reference) => {
        // 色卡（role=style）只管画面色彩和产物过期，不作视频参考图。
        if (reference.shotId !== shotId || reference.status !== "linked" || reference.role === "style") return [];
        const asset = reference.asset || assetByVersionId.get(reference.assetVersionId);
        if (!asset || seenAssetIds.has(asset.id)) return [];
        const image = projectAssetReferenceImage(asset, reference);
        if (!image) return [];
        seenAssetIds.add(asset.id);
        return [{ asset, image, reference }];
    });

    const referenceAudios: ReferenceAudio[] = [];
    const audioIndexByResourceId = new Map<string, number>();
    const assetReferences = entries.map(({ asset }, index) => {
        const sampleResourceId = stringValue(asset.character?.voice?.profile.sampleResourceId);
        let audioIndex: number | undefined;
        if (sampleResourceId) {
            audioIndex = audioIndexByResourceId.get(sampleResourceId);
            if (!audioIndex) {
                audioIndex = referenceAudios.length + 1;
                audioIndexByResourceId.set(sampleResourceId, audioIndex);
                referenceAudios.push({
                    id: `project-character-voice:${asset.id}`,
                    name: `${asset.title}-声音样本`,
                    type: "audio/*",
                    url: resourceFileUrl(sampleResourceId),
                    storageKey: resourceStorageKey(sampleResourceId),
                });
            }
        }
        return {
            assetId: asset.id,
            title: asset.title,
            category: asset.category,
            imageIndex: index + 1,
            ...(audioIndex ? { audioIndex } : {}),
            ...(asset.character ? { voiceDescription: characterVoiceDescription(asset) } : {}),
        } satisfies ShotPromptAssetReference;
    });

    return {
        mentionReferences: entries.map(({ asset, image }) => ({
            id: `project-asset:${asset.id}`,
            nodeId: "",
            assetId: asset.id,
            kind: asset.character ? "character" : "image",
            label: asset.title,
            title: asset.title,
            previewUrl: image.url,
            storageKey: image.storageKey,
            active: true,
            category: projectAssetCategory(asset.category),
        })),
        referenceImages: entries.map(({ image }) => image),
        referenceAudios,
        assetReferences,
        resolvedCharacterVersions: entries.flatMap(({ asset, reference }) => asset.character ? [{ assetId: asset.id, versionId: reference.referencedVersion?.id || reference.assetVersionId || asset.character.versionId }] : []),
    };
}

/**
 * 这一段绑的本场场次色卡（生成分段装段时以 role=style 绑上）：有合成图时给参考图，色值给提示词用；旧版色卡、场次已不存在的不算。
 * 视频参考图里色卡优先级最低：排在角色 / 场景 / 道具之后，名额满了不带图、只把色值写进提示词（withShotScenePalette）。分镜图和黑白预演不带。
 */
export function shotScenePalette(detail: ProjectDetail, shotId: string) {
    const assetByVersionId = new Map(detail.assets.filter((asset) => asset.primaryVersionId).map((asset) => [asset.primaryVersionId as string, asset]));
    for (const reference of detail.shotReferences || []) {
        if (reference.shotId !== shotId || reference.status !== "linked" || reference.role !== "style") continue;
        const asset = reference.asset || assetByVersionId.get(reference.assetVersionId);
        const palette = asset?.palette;
        if (!asset || !palette || palette.master || palette.kind !== "scene" || palette.sceneGone) continue;
        const colors = [...(palette.entry?.shared_colors || []), ...(palette.entry?.new_colors || [])].filter((color) => /^#[0-9A-F]{6}$/i.test(color.hex));
        const image = projectAssetReferenceImage(asset, reference);
        return { code: palette.code, image, colorsLine: colors.length ? `本场色值：${colors.map((color) => `${color.name_zh || color.name_en} ${color.hex.toUpperCase()}`.trim()).join("、")}。` : "" };
    }
    return undefined;
}

/** 把本场色卡加到视频参考图最后：模型的参考图名额（maxImages，没配时按 0 算，不带图）还有空位且有合成图时带图，否则返回要写进提示词的色值行。 */
export function withShotScenePalette(images: ReferenceImage[], palette: ReturnType<typeof shotScenePalette>, maxImages: number) {
    if (!palette) return { images, promptLine: "" };
    if (palette.image && images.length < maxImages) return { images: [...images, palette.image], promptLine: `图片${images.length + 1} 是本场色卡（${palette.code}）：画面用色和光色以它为准，不把色卡本身画进画面。` };
    return { images, promptLine: palette.colorsLine };
}

export function resolveShotAssetMentionPrompt(prompt: string, context: ShotAssetReferenceContext, options: { dialogue?: string } = {}) {
    const imageLabelByAssetId = new Map(context.mentionReferences.map((reference, index) => [reference.assetId, `图片${index + 1}`]));
    const unresolved = new Set<string>();
    const resolved = prompt.replace(/@\[asset:([^\]]+)\]/g, (token, assetId: string) => {
        const label = imageLabelByAssetId.get(assetId);
        if (!label) {
            unresolved.add(token);
            return token;
        }
        return label;
    });
    if (unresolved.size) throw new Error(`提示词中的 ${Array.from(unresolved).join("、")} 未绑定到当前镜头，请重新选择资产或删除引用`);
    const dialogueContext = withShotDialogue(context.assetReferences, options.dialogue);
    const assetBlock = compileShotAssetReferencePrompt(dialogueContext.references);
    const dialogueBlock = dialogueContext.unassignedDialogue ? `【镜头台词】\n\n${dialogueContext.unassignedDialogue}` : "";
    return [resolved.trim(), assetBlock, dialogueBlock].filter(Boolean).join("\n\n");
}

export function ensureShotAssetMentionPrompt(prompt: string, references: Array<Pick<CanvasResourceReference, "assetId" | "kind" | "label" | "title">>) {
    const value = prompt.trim();
    if (!value) return "";
    const mentionedAssetIds = new Set(Array.from(value.matchAll(/@\[asset:([^\]]+)\]/g), (match) => match[1]));
    const missing = references.filter((reference): reference is typeof reference & { assetId: string } => Boolean(reference.assetId && !mentionedAssetIds.has(reference.assetId)));
    if (!missing.length) return value;
    const block = (title: string, items: typeof missing) => items.length
        ? [title, ...items.map((reference) => `${reference.title || reference.label}：@[asset:${reference.assetId}]`)].join("\n")
        : "";
    const characters = missing.filter((reference) => reference.kind === "character");
    const otherAssets = missing.filter((reference) => reference.kind !== "character");
    return [value, block("【角色参考】", characters), block("【场景与道具参考】", otherAssets)].filter(Boolean).join("\n\n");
}

export function compileShotAssetReferencePrompt(references: ShotPromptAssetReference[]) {
    if (!references.length) return "";
    const lines = references.flatMap((reference) => {
        const visualLabel = reference.category === "character" ? "人物参考" : `${projectAssetCategoryLabel(reference.category)}参考`;
        const media = [`${visualLabel}：图片${reference.imageIndex}`, reference.audioIndex ? `声音参考：音频${reference.audioIndex}` : ""].filter(Boolean).join("；");
        return [
            `- ${reference.title}：${media}`,
            reference.voiceDescription ? `  声音画像：${reference.voiceDescription}` : "",
            reference.dialogue ? `  镜头台词：${reference.dialogue}` : "",
        ].filter(Boolean);
    });
    return ["【资产参考】", "", ...lines].join("\n");
}

function projectAssetReferenceImage(asset: ProjectAsset, reference?: ShotAssetReference): ReferenceImage | undefined {
    const boundRepresentations = reference?.referencedVersion?.representations || [];
    const representation = preferredVisualRepresentation(boundRepresentations)
        || (asset.character ? preferredVisualRepresentation(asset.character.representations) : undefined);
    if (representation) {
        return {
            id: asset.id,
            name: asset.title,
            type: "image/*",
            dataUrl: "",
            url: resourceFileUrl(representation.resourceId),
            storageKey: resourceStorageKey(representation.resourceId),
        };
    }
    if (asset.mediaType !== "image" || !asset.storageKey) return undefined;
    const resourceId = resourceIdFromStorageKey(asset.storageKey);
    return {
        id: asset.id,
        name: asset.title,
        type: "image/*",
        dataUrl: "",
        ...(resourceId ? { url: resourceFileUrl(resourceId) } : {}),
        storageKey: asset.storageKey,
    };
}

function preferredVisualRepresentation(representations: CharacterRepresentation[]) {
    return representations.find((item) => item.role === "turnaround_sheet")
        || representations.find((item) => item.role === "primary")
        || representations.find((item) => item.role === "front")
        || representations.find((item) => item.mediaType.startsWith("image"));
}

function withShotDialogue(references: ShotPromptAssetReference[], dialogue?: string) {
    const value = stringValue(dialogue);
    if (!value) return { references, unassignedDialogue: "" };
    const characters = references.filter((reference) => reference.category === "character");
    if (characters.length === 1) {
        return {
            references: references.map((reference) => reference.assetId === characters[0]?.assetId ? { ...reference, dialogue: value } : reference),
            unassignedDialogue: "",
        };
    }
    const resolved = references.map((reference) => {
        if (reference.category !== "character") return reference;
        const matched = dialogueForCharacter(value, reference.title);
        return matched ? { ...reference, dialogue: matched } : reference;
    });
    return {
        references: resolved,
        unassignedDialogue: resolved.some((reference) => reference.dialogue) ? "" : value,
    };
}

function dialogueForCharacter(dialogue: string, characterName: string) {
    const escaped = characterName.replace(/[.*+?^${}()|[\]\\]/g, "\\$&");
    const pattern = new RegExp(`(?:^|\\n)\\s*${escaped}\\s*[：:]\\s*([^\\n]+)`, "g");
    return Array.from(dialogue.matchAll(pattern), (match) => match[1]?.trim()).filter(Boolean).join("；");
}

function projectAssetCategory(value: string): AssetCategory {
    return normalizeAssetCategory(value);
}

function projectAssetCategoryLabel(value: string) {
    return assetCategoryLabel(value);
}

function characterVoiceDescription(asset: ProjectAsset) {
    const definition = asset.character?.definition || {};
    const voice = asset.character?.voice;
    return [
        stringValue(definition.voiceLanguage) || stringValue(voice?.profile.language),
        stringValue(definition.voiceAge),
        stringValue(definition.voiceTimbre) || stringValue(voice?.profile.timbre),
        stringValue(voice?.instructions),
    ].filter(Boolean).join("；");
}

function stringValue(value: unknown) {
    return typeof value === "string" ? value.trim() : "";
}

// 编译清单的编号按模型方言写：即梦是 @图片N / @音频N，MiniMax-H3 是 <Picture N> / <Audio N>（提示词原样发给模型，编号不能改写）。
const COMPILED_IMAGE_TAG = /^(?:@图片\d+|<Picture \d+>)$/;
const COMPILED_AUDIO_TAG = /^(?:@音频\d+|<Audio \d+>)$/;

/**
 * 把方言编译结果里的素材清单（@图片N / <Picture N>、@音频N / <Audio N>）换成生成任务的参考媒体，顺序与编号一致。
 * 契约里 file 是柚子资产 ID 或资产版本 ID；找不到的素材返回在 missing 里，由调用方拦下。
 */
export function referenceMediaForCompiledFiles(detail: ProjectDetail, shotId: string, files: Array<{ file: string; tag: string; role: string }>) {
    const references = (detail.shotReferences || []).filter((reference) => reference.shotId === shotId && reference.status === "linked");
    const findAsset = (file: string) => {
        const reference = references.find((item) => item.assetVersionId === file || item.asset?.id === file || item.referencedVersion?.id === file);
        const asset = reference?.asset || detail.assets.find((item) => item.id === file || item.primaryVersionId === file);
        return asset ? { asset, reference } : undefined;
    };
    const images: ReferenceImage[] = [];
    const audios: ReferenceAudio[] = [];
    const missing: string[] = [];
    for (const item of files) {
        const found = findAsset(item.file);
        if (COMPILED_IMAGE_TAG.test(item.tag)) {
            const image = found ? projectAssetReferenceImage(found.asset, found.reference) : undefined;
            if (image) images.push(image);
            else missing.push(`${item.tag}（${item.file}）`);
        } else if (COMPILED_AUDIO_TAG.test(item.tag)) {
            const sampleResourceId = stringValue(found?.asset.character?.voice?.profile.sampleResourceId);
            if (found && sampleResourceId) audios.push({ id: `project-character-voice:${found.asset.id}`, name: `${found.asset.title}-声音样本`, type: "audio/*", url: resourceFileUrl(sampleResourceId), storageKey: resourceStorageKey(sampleResourceId) });
            else missing.push(`${item.tag}（${item.file}）`);
        } else {
            missing.push(`${item.tag}（${item.file}，工作台暂不支持视频参考）`);
        }
    }
    return { images, audios, missing };
}
