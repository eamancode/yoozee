import { getActiveUserScope } from "@/lib/user-scope";
import { useDramaModelPreferencesStore } from "@/stores/use-drama-model-preferences-store";
import { queueDramaAssetImages as queueImages } from "./api/project-drama";
import { generateStyleProfileCover as generateCover } from "./api/style-profiles";

export async function queueDramaAssetImages(...args: Parameters<typeof queueImages>) {
    const scope = getActiveUserScope();
    const result = await queueImages(...args);
    if (result.queued.length) useDramaModelPreferencesStore.getState().remember("image", args[1].imageTask.model, scope);
    return result;
}

export async function generateStyleProfileCover(...args: Parameters<typeof generateCover>) {
    const scope = getActiveUserScope();
    const result = await generateCover(...args);
    if (result.profile.coverTaskId && ["queued", "running"].includes(result.profile.coverStatus || "")) useDramaModelPreferencesStore.getState().remember("image", args[1].model, scope);
    return result;
}
