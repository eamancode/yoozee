import { useMemo } from "react";
import { useEffectiveConfig } from "@/stores/use-config-store";
import { useDramaModelPreferencesStore } from "@/stores/use-drama-model-preferences-store";
import { preferredDramaModel } from "@/lib/drama-model-preferences";

export function useDramaModelConfig() {
    const config = useEffectiveConfig();
    const models = useDramaModelPreferencesStore((state) => state.models);
    return useMemo(() => ({ ...config,
        textModel: preferredDramaModel(config, "text", models),
        imageModel: preferredDramaModel(config, "image", models),
        videoModel: preferredDramaModel(config, "video", models),
    }), [config, models]);
}
