import { useQuery } from "@tanstack/react-query";
import { listDramaModels, type DramaAuthMode, type DramaModel, type DramaRegistry } from "@/services/api/drama";

/** models.json 的默认值；目录还没读到时用它占位，最终以服务端校验为准。 */
export const DEFAULT_DRAMA_MODEL = "seedance-2.0";

export const DRAMA_AUTH_OPTIONS: Array<{ label: string; value: DramaAuthMode; hint: string }> = [
    { label: "每步确认", value: "step", hint: "每个节点交付后停下来等你确认" },
    { label: "全权", value: "full", hint: "一路做到交付，只在超预算、删文件、对外发布、用你的账号时找你" },
];

export function useDramaModels() {
    return useQuery({ queryKey: ["drama", "models"], queryFn: async () => (await listDramaModels()).models, staleTime: 10 * 60_000 });
}

export function dramaModelOptions(registry?: DramaRegistry) {
    if (!registry?.models?.length) return [{ label: "Seedance 2.0 · 每段最长 15 秒", value: DEFAULT_DRAMA_MODEL }];
    return registry.models.map((item) => ({ label: `${item.label} · 每段最长 ${item.duration.max} 秒${item.calibrated ? "" : " · 未校准"}`, value: item.id }));
}

export function findDramaModel(registry: DramaRegistry | undefined, id: string | undefined): DramaModel | undefined {
    if (!registry) return undefined;
    return registry.models.find((item) => item.id === (id || registry.default)) || registry.models.find((item) => item.id === registry.default);
}

export function dramaAuthLabel(mode: string | undefined) {
    return mode === "full" ? "全权" : "每步确认";
}
