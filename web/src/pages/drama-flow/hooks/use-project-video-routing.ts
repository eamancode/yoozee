import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";

import { getProjectVideoRouting, overrideProjectVideoRoute, projectVideoRoutingKey, type ProjectVideoOverride, type ProjectVideoRouting } from "@/services/api/project-video-routing";

export function useProjectVideoRouting(projectId: string, watch: boolean) {
    const client = useQueryClient();
    const query = useQuery({
        queryKey: projectVideoRoutingKey(projectId),
        queryFn: ({ signal }) => getProjectVideoRouting(projectId, signal),
        enabled: Boolean(projectId),
        refetchOnMount: "always",
        refetchInterval: watch ? 5000 : false,
    });
    const change = useMutation({
        mutationFn: ({ id, input }: { id: string; input: ProjectVideoOverride }) => overrideProjectVideoRoute(id, input),
        onSuccess: async (result, { id }) => {
            const key = projectVideoRoutingKey(id);
            await client.cancelQueries({ queryKey: key });
            client.setQueryData<ProjectVideoRouting>(key, (current) => ((current?.route?.revision || 0) > (result.route?.revision || 0) ? current : result));
        },
        onError: (_error, { id }) => client.invalidateQueries({ queryKey: projectVideoRoutingKey(id) }),
    });
    const saving = change.isPending && change.variables.id === projectId;
    const issue = saving ? "正在保存项目视频线路…" : query.isError ? "项目视频线路没读到，请重新读取" : query.data?.route === undefined ? "正在读取项目视频线路…" : "";
    return {
        route: query.data?.route,
        issue,
        saveError: change.isError && change.variables.id === projectId ? change.error.message : "",
        retry: query.isError ? () => void query.refetch() : undefined,
        select: (model: string, fromModel: string) => {
            if (issue || !model) return;
            change.mutate({ id: projectId, input: { model, fromModel, revision: query.data?.route?.revision || 0 } });
        },
    };
}
