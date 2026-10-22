import type { DramaBible, DramaBibleCharacter, DramaBibleLocation, DramaBibleProp, DramaBibleVariant } from "@/services/api/drama";
import type { ProjectAsset } from "@/services/api/projects";

export type ChecklistKind = "character" | "location" | "prop";

export type ChecklistCheck = { label: string; ok: boolean; detail: string };

export type ChecklistItem = {
    /** 在连戏表里的位置，勾选时按它写回：character:0:1 = 第 1 个人物的第 2 个造型。 */
    key: string;
    kind: ChecklistKind;
    name: string;
    variant?: string;
    asset?: ProjectAsset;
    lockCard: string[];
    voice?: string;
    voiceShort?: string;
    /** 机器能查的几项；全过才允许人判「过门禁」。 */
    checks: ChecklistCheck[];
    ready: boolean;
    checked: boolean;
};

const LOCK_CARD_MIN = 8;
const LOCK_CARD_MAX = 12;

/** 连戏表里的文件在柚子里是资产版本 ID；也接受直接写资产名的旧写法。 */
function findAsset(assets: ProjectAsset[], name: string, files: Array<string | undefined>) {
    const ids = new Set(files.filter((file): file is string => Boolean(file && file.trim())).map((file) => file.trim()));
    return assets.find((asset) => asset.primaryVersionId && ids.has(asset.primaryVersionId)) || assets.find((asset) => asset.title.trim() === name.trim());
}

function filled(value: unknown) {
    return typeof value === "string" && value.trim().length > 0;
}

function characterChecks(character: DramaBibleCharacter, variant: DramaBibleVariant, asset?: ProjectAsset): ChecklistCheck[] {
    const lockCard = (variant.lock_card || []).filter((item) => item.trim());
    const sheetOk = filled(variant.sheet) || asset?.character?.visualStatus === "ready";
    const checks: ChecklistCheck[] = [
        { label: "三视图", ok: sheetOk, detail: sheetOk ? "已有三视图" : "还没有三视图：用 yz-cast 生成定妆照，或在角色卡上绑定" },
        {
            label: "锁定卡",
            ok: lockCard.length >= LOCK_CARD_MIN && lockCard.length <= LOCK_CARD_MAX,
            detail: lockCard.length ? `${lockCard.length} 项（要 ${LOCK_CARD_MIN}～${LOCK_CARD_MAX} 项具体视觉短语）` : `还没写（要 ${LOCK_CARD_MIN}～${LOCK_CARD_MAX} 项具体视觉短语）`,
        },
    ];
    if (character.speaks !== false) {
        const voiceOk = filled(variant.voice) || asset?.character?.voiceStatus === "ready";
        checks.push({ label: "音色", ok: voiceOk, detail: voiceOk ? (filled(variant.voice_short) ? "长版、短版都有" : "有长版；多人对戏时最好补短版") : "有台词的角色要固定音色参考" });
    }
    return checks;
}

/** 把连戏表展开成一张素材清单：人物按造型一行，场景、道具各一行；每行带机器能查的门禁和人判的「过门禁」。 */
export function buildAssetChecklist(bible: DramaBible | undefined, assets: ProjectAsset[]): ChecklistItem[] {
    const items: ChecklistItem[] = [];
    (bible?.characters || []).forEach((character, characterIndex) => {
        (character.variants || []).forEach((variant, variantIndex) => {
            const asset = findAsset(
                assets.filter((item) => item.category === "character"),
                character.name,
                [variant.sheet, ...(variant.crops || [])],
            );
            const checks = characterChecks(character, variant, asset);
            items.push({
                key: `character:${characterIndex}:${variantIndex}`,
                kind: "character",
                name: character.name,
                variant: (character.variants || []).length > 1 || variant.label !== "默认" ? variant.label : undefined,
                asset,
                lockCard: (variant.lock_card || []).filter((item) => item.trim()),
                voice: variant.voice,
                voiceShort: variant.voice_short,
                checks,
                ready: checks.every((check) => check.ok),
                checked: Boolean(variant.checked),
            });
        });
    });
    (bible?.locations || []).forEach((location: DramaBibleLocation, index) => {
        const asset = findAsset(
            assets.filter((item) => item.category === "environment"),
            location.name,
            [location.plate, location.side_a, location.side_b],
        );
        const plateOk = filled(location.plate);
        const checks: ChecklistCheck[] = [{ label: "无人空景", ok: plateOk, detail: plateOk ? (filled(location.side_a) && filled(location.side_b) ? "空景与正反打背景都有" : "有空景；有对话的场景最好补正反打背景") : "还没有无人空景图" }];
        items.push({ key: `location:${index}`, kind: "location", name: location.name, asset, lockCard: [], checks, ready: plateOk, checked: Boolean(location.checked) });
    });
    (bible?.props || []).forEach((prop: DramaBibleProp, index) => {
        const asset = findAsset(
            assets.filter((item) => item.category === "prop"),
            prop.name,
            [prop.image, ...(prop.states || []).map((state) => state.image)],
        );
        const imageOk = filled(prop.image);
        const checks: ChecklistCheck[] = [{ label: "道具图", ok: imageOk, detail: imageOk ? `有道具图${(prop.states || []).length ? `，${(prop.states || []).length} 个状态` : ""}` : "还没有道具图" }];
        items.push({ key: `prop:${index}`, kind: "prop", name: prop.name, asset, lockCard: [], checks, ready: imageOk, checked: Boolean(prop.checked) });
    });
    return items;
}

/** 勾选或取消「过门禁」，返回新的连戏表；不改其他字段。 */
export function setChecklistChecked(bible: DramaBible, key: string, checked: boolean): DramaBible {
    const [kind, first, second] = key.split(":");
    const index = Number(first);
    if (kind === "character") {
        const characters = (bible.characters || []).map((character, characterIndex) =>
            characterIndex !== index
                ? character
                : {
                      ...character,
                      variants: character.variants.map((variant, variantIndex) => (variantIndex === Number(second) ? { ...variant, checked } : variant)),
                  },
        );
        return { ...bible, characters };
    }
    if (kind === "location") return { ...bible, locations: (bible.locations || []).map((item, itemIndex) => (itemIndex === index ? { ...item, checked } : item)) };
    if (kind === "prop") return { ...bible, props: (bible.props || []).map((item, itemIndex) => (itemIndex === index ? { ...item, checked } : item)) };
    return bible;
}

/** 连戏表还空着时，按项目里已确认的角色、场景、道具起草清单；文件写资产版本 ID，checked 一律 false 等人判。 */
export function draftBibleFromAssets(bible: DramaBible | undefined, assets: ProjectAsset[]): DramaBible {
    const characters: DramaBibleCharacter[] = assets
        .filter((asset) => asset.category === "character")
        .map((asset) => ({
            name: asset.title,
            variants: [{ label: "默认", ...(asset.character?.visualStatus === "ready" && asset.primaryVersionId ? { sheet: asset.primaryVersionId } : {}), checked: false }],
        }));
    const locations: DramaBibleLocation[] = assets.filter((asset) => asset.category === "environment").map((asset) => ({ name: asset.title, ...(asset.primaryVersionId ? { plate: asset.primaryVersionId } : {}), checked: false }));
    const props: DramaBibleProp[] = assets.filter((asset) => asset.category === "prop").map((asset) => ({ name: asset.title, ...(asset.primaryVersionId ? { image: asset.primaryVersionId } : {}), checked: false }));
    return { ...(bible || {}), characters, locations, props };
}

export function checklistSummary(items: ChecklistItem[]) {
    return { total: items.length, checked: items.filter((item) => item.checked).length, ready: items.filter((item) => item.ready).length };
}
