import { expect, test } from "bun:test";
import axios from "axios";

import { createProjectWithUniqueName } from "@/pages/projects/workbench/create/create-project-flow";
import { apiClient } from "@/services/api/request";
import type { CreateProjectInput } from "@/services/api/projects";

test("account cancellation prevents a name-conflict retry", async () => {
    const previous = apiClient.defaults.adapter;
    const controller = new AbortController();
    let calls = 0;
    apiClient.defaults.adapter = async (config) => {
        calls++;
        controller.abort();
        throw new axios.AxiosError("Conflict", "ERR_BAD_REQUEST", config, undefined, {
            status: 409,
            statusText: "Conflict",
            headers: {},
            config,
            data: { code: 409, reason: "project_name_conflict", msg: "项目名已存在" },
        });
    };
    try {
        await expect(createProjectWithUniqueName({ name: "old account" } as CreateProjectInput, controller.signal)).rejects.toMatchObject({ name: "AbortError" });
        expect(calls).toBe(1);
    } finally {
        apiClient.defaults.adapter = previous;
    }
});
