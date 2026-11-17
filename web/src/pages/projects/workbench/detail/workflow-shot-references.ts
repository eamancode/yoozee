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
	spatialMaster?: boolean;
    assetId: string;
    title: string;
    category: string;
    imageIndex: number;
    audioIndex?: number;
    voiceDescription?: string;
    dialogue?: string;
};

export function shotAssetReferences(detail: Pick<ProjectDetail, "shotReferences">, shotId: string) {
    return (detail.shotReferences || []).filter((ref) => ref.shotId === shotId && ref.status === "linked")
        .sort((a, b) => a.source === "segment" && b.source === "segment" ? (a.position || 0) - (b.position || 0) : a.source === "segment" ? -1 : b.source === "segment" ? 1 : 0);
}

export function projectAssetChoices(assets: ProjectAsset[]) {
    const byId = new Map<string, ProjectAsset>();
    for (const asset of assets) { byId.set(asset.id, asset); for (const variant of asset.variants || []) byId.set(variant.id, variant); }
    return Array.from(byId.values());
}

/** 分段绑定指定版本后，图片和声音均从该版本读取，包括该版本没有音样的情况。 */
export function projectAssetVoiceSampleResourceId(asset: ProjectAsset, reference?: ShotAssetReference) {
    if (asset.category !== "character") return "";
    if (reference?.referencedVersion) return reference.referencedVersion.id === reference.assetVersionId ? stringValue(reference.referencedVersion.sampleResourceId) : "";
    if (reference && reference.assetVersionId !== asset.primaryVersionId) return "";
    return stringValue(asset.character?.voice?.profile.sampleResourceId);
}

export function buildShotAssetReferenceContext(detail: ProjectDetail, shotId: string): ShotAssetReferenceContext {
    const assetByVersionId = new Map(detail.assets.filter((asset) => asset.primaryVersionId).map((asset) => [asset.primaryVersionId as string, asset]));
    const seenVersions = new Set<string>();
    const entries = shotAssetReferences(detail, shotId).flatMap((reference) => {
        // 色卡（role=style）只管画面色彩和产物过期，不作视频参考图。
        if (reference.shotId !== shotId || reference.status !== "linked" || reference.role === "style" || reference.role === "continuity") return [];
        const asset = reference.asset || assetByVersionId.get(reference.assetVersionId);
        if (!asset || asset.category === "palette" || seenVersions.has(reference.assetVersionId)) return [];
        const image = projectAssetReferenceImage(asset, reference);
        if (!image) return [];
        seenVersions.add(reference.assetVersionId);
        return [{ asset, image, reference }];
    });

    const referenceAudios: ReferenceAudio[] = [];
    const audioIndexByResourceId = new Map<string, number>();
    const assetReferences = entries.map(({ asset, reference }, index) => {
        const sampleResourceId = projectAssetVoiceSampleResourceId(asset, reference);
        let audioIndex: number | undefined;
        if (sampleResourceId) {
            audioIndex = audioIndexByResourceId.get(sampleResourceId);
            if (!audioIndex) {
                audioIndex = referenceAudios.length + 1;
                audioIndexByResourceId.set(sampleResourceId, audioIndex);
                referenceAudios.push({
                    id: sampleResourceId,
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
			...(asset.visual?.definition.imageLayout === "spatial-master" ? { spatialMaster: true } : {}),
            ...(audioIndex ? { audioIndex } : {}),
            ...(asset.character ? { voiceDescription: characterVoiceDescription(asset) } : {}),
        } satisfies ShotPromptAssetReference;
    });

    // @ 提及指向资产；同一资产的多个固定版本都有独立媒体编号，选择器只出现一次。
    const mentions = new Map<string, CanvasResourceReference>();
    for (const { asset, image } of entries) {
        if (mentions.has(asset.id)) continue;
        mentions.set(asset.id, {
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
        });
    }
    return {
        mentionReferences: Array.from(mentions.values()),
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
    const imageLabelByAssetId = new Map<string, string>();
    for (const reference of context.assetReferences) {
        if (!imageLabelByAssetId.has(reference.assetId)) imageLabelByAssetId.set(reference.assetId, `图片${reference.imageIndex}`);
    }
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

export function changeShotAssetMentionPrompt(prompt: string, previous: Pick<ProjectAsset, "id" | "title">, next?: Pick<ProjectAsset, "id" | "title">) {
    const oldToken = `@[asset:${previous.id}]`;
    const nextToken = next ? `@[asset:${next.id}]` : "";
    // 自动补入的参考行更新名称；正文只替换标识，取消时保留资产名称和原有动作。
    return prompt.split("\n").map((line) => line.trim() === `${previous.title}：${oldToken}`
        ? next ? `${next.title}：${nextToken}` : ""
        : line.split(oldToken).join(nextToken || previous.title)).join("\n");
}

export function compileShotAssetReferencePrompt(references: ShotPromptAssetReference[]) {
    if (!references.length) return "";
    const lines = references.flatMap((reference) => {
        const visualLabel = reference.category === "character" ? "人物参考" : `${projectAssetCategoryLabel(reference.category)}参考`;
        const media = [`${visualLabel}：图片${reference.imageIndex}`, reference.audioIndex ? `声音参考：音频${reference.audioIndex}` : ""].filter(Boolean).join("；");
        return [
            `- ${reference.title}：${media}`,
			reference.spatialMaster ? "  空间布局母版：保持几何与道具位置；本镜使用真实尺度，不继承微缩底座、模型观感或母版俯视机位。" : "",
            reference.voiceDescription ? `  声音画像：${reference.voiceDescription}` : "",
            reference.dialogue ? `  镜头台词：${reference.dialogue}` : "",
        ].filter(Boolean);
    });
    return ["【资产参考】", "", ...lines].join("\n");
}

export function projectAssetReferenceImage(asset: ProjectAsset, reference?: ShotAssetReference): ReferenceImage | undefined {
    if (reference?.referencedVersion && reference.referencedVersion.id !== reference.assetVersionId) return undefined;
    const boundImage = preferredVisualRepresentation(reference?.referencedVersion?.representations || []);
    if (reference && reference.assetVersionId !== asset.primaryVersionId && !boundImage) return undefined;
    const representation = boundImage
        || (asset.character ? preferredVisualRepresentation(asset.character.representations) : undefined);
    const id = reference && reference.assetVersionId !== asset.primaryVersionId ? reference.assetVersionId : asset.id;
    if (representation) {
        return {
            id,
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
        id,
        name: asset.title,
        type: "image/*",
        dataUrl: "",
        ...(resourceId ? { url: resourceFileUrl(resourceId) } : {}),
        storageKey: asset.storageKey,
    };
}

function preferredVisualRepresentation(representations: CharacterRepresentation[] = []) {
    const images = representations.filter((item) => item.resourceId && item.mediaType?.startsWith("image"));
    return images.find((item) => item.role === "turnaround_sheet")
        || images.find((item) => item.role === "primary")
        || images.find((item) => item.role === "front")
        || images[0];
}

function withShotDialogue(references: ShotPromptAssetReference[], dialogue?: string) {
    const value = stringValue(dialogue);
    if (!value) return { references, unassignedDialogue: "" };
    const characters = references.filter((reference) => reference.category === "character");
    if (new Set(characters.map((character) => character.assetId)).size === 1) {
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
const COMPILED_VIDEO_TAG = /^@视频\d+$/;
// MiniMax-H3 锁首帧段的编号就是输入通道名：首尾帧走图生视频通道（content 里 role=first_frame / last_frame），
// 和参考通道（reference_*）互斥，不能当参考图发。即梦锁首帧段的编号是 @图片1（role first_frame），仍按参考图提交。
const COMPILED_FRAME_TAG: Record<string, { role: "first_frame" | "last_frame"; label: string }> = {
    first_frame: { role: "first_frame", label: "首帧" },
    last_frame: { role: "last_frame", label: "尾帧" },
};

/**
 * 把方言编译结果里的素材清单换成生成任务的媒体，顺序与编号一致：@图片N / <Picture N> 是参考图，@音频N / <Audio N> 是参考音频，
 * first_frame / last_frame 是首尾帧（图片带 role，frames=true，调用方按图生视频提交）。
 * 图片 file 是资产或版本 ID，声音 file 是这段已绑定角色版本的音样资源 ID（旧清单可能给版本 ID）。
 * 找不到的素材返回在 missing 里，工作台提交不了的写在 unsupported 里，都由调用方拦下。
 */
export function referenceMediaForCompiledFiles(detail: Pick<ProjectDetail, "assets" | "shotReferences">, shotId: string, files: Array<{ file: string; tag: string; role: string }>) {
    const references = shotAssetReferences(detail, shotId);
    const findAsset = (file: string) => {
        const reference = references.find((item) => item.assetVersionId === file || item.asset?.id === file || item.referencedVersion?.id === file);
        const asset = reference?.asset || detail.assets.find((item) => item.id === file || item.primaryVersionId === file);
        return asset ? { asset, reference } : undefined;
    };
    const images: ReferenceImage[] = [];
    const audios: ReferenceAudio[] = [];
    const missing: string[] = [];
    const unsupported: string[] = [];
    const boundSamples = new Set<string>();
    for (const reference of references) {
        if (reference.role === "style" || reference.role === "continuity") continue;
        const asset = reference.asset || detail.assets.find((item) => item.primaryVersionId === reference.assetVersionId);
        if (!asset) continue;
        const sample = projectAssetVoiceSampleResourceId(asset, reference);
        if (sample) boundSamples.add(sample);
    }
    let frames = 0;
    for (const item of files) {
        const found = findAsset(item.file);
        const frame = COMPILED_FRAME_TAG[item.tag];
        if (frame) {
            const image = found ? projectAssetReferenceImage(found.asset, found.reference) : undefined;
            if (image) {
                images.push({ ...image, role: frame.role });
                frames += 1;
            } else missing.push(`${frame.label}（${item.file}）`);
        } else if (COMPILED_IMAGE_TAG.test(item.tag)) {
            const image = found ? projectAssetReferenceImage(found.asset, found.reference) : undefined;
            if (image) images.push(image);
            else missing.push(`${item.tag}（${item.file}）`);
        } else if (COMPILED_AUDIO_TAG.test(item.tag)) {
            const versionSample = found?.reference?.role === "reference" ? projectAssetVoiceSampleResourceId(found.asset, found.reference) : "";
            const sampleResourceId = boundSamples.has(item.file) ? item.file : versionSample;
            if (sampleResourceId) {
                audios.push({ id: sampleResourceId, name: item.tag, type: "audio/*", url: resourceFileUrl(sampleResourceId), storageKey: resourceStorageKey(sampleResourceId) });
            } else missing.push(`${item.tag}（${item.file}）`);
        } else if (COMPILED_VIDEO_TAG.test(item.tag)) {
            unsupported.push(`${item.tag} 是参考视频，工作台暂不支持提交参考视频`);
        } else {
            unsupported.push(`工作台不认识编译清单里的编号 ${item.tag}（${item.file}）`);
        }
    }
    // 首尾帧和参考素材是两条互斥的输入通道（frames_exclusive_with_refs），同一个请求只能选一条。
    if (frames && (frames < images.length || audios.length)) unsupported.push("首尾帧不能和参考图、参考音频一起提交，请去掉这一段的参考或改用锁人物模式");
    return { images, audios, frames: frames > 0, missing, unsupported };
}

/** 资产的缩略图：角色取主图（没有取第一张形象图），其余取图片资源；没有图时为空。 */
export function assetPreviewUrl(asset: ProjectAsset) {
    const representation = asset.character?.representations?.find((item) => item.role === "primary") || asset.character?.representations?.[0];
    if (representation?.resourceId) return resourceFileUrl(representation.resourceId);
    const resourceId = resourceIdFromStorageKey(asset.storageKey);
    return resourceId && asset.mediaType === "image" ? resourceFileUrl(resourceId) : "";
}
