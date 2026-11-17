import { preferredDramaModel } from "@/lib/drama-model-preferences";
import { dramaFamilyOf, projectVideoModel } from "@/pages/projects/workbench/drama-model";
import type { DramaModel, DramaRegistry } from "@/services/api/project-drama";
import { configuredModelMatchesCapability, modelDisplayName, selectableModelsByCapability, type AiConfig } from "@/stores/use-config-store";
import type { DramaRecentModels } from "@/stores/use-drama-model-preferences-store";

import type { ModelName } from "../hooks/use-model-names";
import type { ReadState } from "./read-state";

/**
 * 新入口各步骤用的模型：一律「上次用的 > 项目默认 > 全局默认 > 第一个可用」，并且只取这一步能用的（accepts）。
 * 步骤页的模型卡、确认面板和真正提交都从这里取，改了模型卡就是改「上次用的」。
 */

type Accepts = (model: string) => boolean;
const always: Accepts = () => true;

/** 资产出图：上次用的图片模型优先，项目设置里的默认图片模型只作回退（和旧工作台的角色变体同一口径）。 */
export function flowImageModel(config: AiConfig, recent: DramaRecentModels, projectDefault?: string, accepts: Accepts = always) {
    return preferredDramaModel(config, "image", recent, projectDefault, accepts);
}

/** 文本步骤（拆剧本、定画风、色卡、分镜）：上次用的优先，回退规则同旧工作台。 */
export function flowTextModel(config: AiConfig, recent: DramaRecentModels, accepts: Accepts = always) {
    return preferredDramaModel(config, "text", recent, undefined, accepts);
}

/**
 * 出片：上次用的视频模型只要还能用就直接用，不限于项目生成模型那一族；
 * 用不了时回到旧规则（项目默认 > 同族第一个 > 全局默认），再不行取能用的里同族优先的第一个。
 */
export function flowVideoModel(config: AiConfig, recent: DramaRecentModels, projectDefault: string | undefined, registry: DramaRegistry | undefined, family: DramaModel | undefined, accepts: Accepts = always) {
    const usable = (model?: string): model is string => Boolean(model && configuredModelMatchesCapability(config, model, "video") && accepts(model));
    const last = recent.video?.trim();
    if (usable(last)) return last;
    const legacy = projectVideoModel(config, projectDefault, registry, family);
    if (usable(legacy)) return legacy;
    const options = selectableModelsByCapability(config, "video").filter(usable);
    return options.find((model) => family && dramaFamilyOf(registry, model, modelDisplayName(config, model))?.id === family.id) || options[0] || "";
}

/** 所选视频模型不属于项目生成模型那一族时，返回它所属的族（认不出族时返回 null）；同族或没选时返回 undefined。 */
export function videoFamilyMismatch(config: AiConfig, model: string, registry: DramaRegistry | undefined, family: DramaModel | undefined): DramaModel | null | undefined {
    if (!model || !family || !registry) return undefined;
    const actual = dramaFamilyOf(registry, model, modelDisplayName(config, model));
    if (actual?.id === family.id) return undefined;
    return actual || null;
}

/**
 * 模型卡、信息面板上写的状态。chosen 是用户原来选的（上次用的或项目默认，未校验），effective 是这次实际会提交的。
 * 顺序：
 * 1. 没原选择：写实际会用的模型（没有就写 unset）。
 * 2. 模型目录确认原选择已下架：有合法回退写「已不可用，现在改用 X」，没有就让人重选。
 * 3. 目录在读或没读到：照实写目录状态，不说「已不可用」，也不把缓存名字当成已确认。
 * 4. 这一步不能用（个人渠道、平台不可用、画幅不支持）：写原因；有回退写改用了谁。
 * 5. 原选择能用但实际提交的是别的（如本地配置里没有它）：写明改用了谁；什么都没有就写 unset。
 */
export function stepModelStatus(input: { chosen: string; effective: string; catalogRead: ReadState; nameOf: (id: string | undefined, unset: string) => ModelName; disabledReason: (model: string) => string; unset: string }): ModelName {
    const { chosen, effective, catalogRead, nameOf, disabledReason, unset } = input;
    if (!chosen && !effective && catalogRead !== "ready") {
        return catalogRead === "loading" ? { text: "正在读模型目录…" } : { text: "模型目录没读到，没法确认现在是否可用", tone: "warn" };
    }
    if (!chosen) return nameOf(effective, unset);
    const own = nameOf(chosen, unset);
    const fallback = effective && effective !== chosen ? nameOf(effective, unset).text : "";
    if (own.tone === "bad") return fallback ? { text: `原来选的模型已不可用，现在改用 ${fallback}`, tone: "warn" } : own;
    if (catalogRead !== "ready") return own;
    const reason = disabledReason(chosen);
    if (reason) return fallback ? { text: `原来选的模型${reason}，现在改用 ${fallback}`, tone: "warn" } : { text: `原来选的模型${reason}，点这里重选`, tone: "bad" };
    if (effective === chosen) return own;
    if (!effective) return nameOf("", unset);
    return { text: `原来选的${own.text}这一步用不了，现在改用 ${fallback}`, tone: "warn" };
}
