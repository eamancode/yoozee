import { useQuery } from "@tanstack/react-query";
import { getDramaProgress } from "@/services/api/project-drama";
import { dramaProgressKey } from "./drama-panels";

export function useProjectPipeline(projectId: string) {
    return useQuery({
        queryKey: dramaProgressKey(projectId),
        queryFn: async ({ signal }) => (await getDramaProgress(projectId, signal)).progress,
        enabled: Boolean(projectId),
        refetchInterval: (query) => {
            const pipeline = query.state.data?.pipeline;
            if (!pipeline) return false;
            if (!pipeline.stop) return pipeline.current ? 8000 : false;
            return pipeline.stop.continuable ? 20000 : false;
        },
    });
}
