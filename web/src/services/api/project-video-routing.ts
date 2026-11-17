import { http } from "./request";

export interface ProjectVideoRoute {
    status: "auto" | "manual" | "unavailable";
    fromModel: string;
    model: string;
    reason: string;
    successRate: number | null;
    sampledAt: string | null;
    changedAt: string;
    revision: number;
}

export type ProjectVideoRouting = { route: ProjectVideoRoute | null };
export type ProjectVideoOverride = { model: string; fromModel: string; revision: number };

const routingPath = (projectId: string) => `/projects/${encodeURIComponent(projectId)}/drama/video-routing`;
export const projectVideoRoutingKey = (projectId: string) => ["project", projectId, "drama", "video-routing"] as const;
export const getProjectVideoRouting = (projectId: string, signal?: AbortSignal) => http.get<ProjectVideoRouting>(routingPath(projectId), { signal });
export const overrideProjectVideoRoute = (projectId: string, input: ProjectVideoOverride) => http.post<ProjectVideoRouting>(`${routingPath(projectId)}/override`, input);
