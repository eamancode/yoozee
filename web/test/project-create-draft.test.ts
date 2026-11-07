import { expect, test } from "bun:test";
import localforage from "localforage";

import { setActiveUserScope } from "@/lib/user-scope";
import { clearProjectDraft, EMPTY_PROJECT_DRAFT, loadProjectDraft, saveProjectDraft } from "@/pages/projects/workbench/create/project-draft";

test("切号后到达的建项草稿保存、读取和清理始终属于原账号", async () => {
    const windowDescriptor = Object.getOwnPropertyDescriptor(globalThis, "window");
    const original = { getItem: localforage.getItem, setItem: localforage.setItem, removeItem: localforage.removeItem };
    const records = new Map<string, string>();
    const preferences = new Map<string, string>();
    Object.defineProperty(globalThis, "window", { configurable: true, value: { localStorage: { getItem: (key: string) => preferences.get(key), setItem: (key: string, value: string) => preferences.set(key, value) } } });
    localforage.getItem = (async (key: string) => records.get(key) || null) as typeof localforage.getItem;
    localforage.setItem = (async (key: string, value: string) => {
        records.set(key, value);
        return value;
    }) as typeof localforage.setItem;
    localforage.removeItem = async (key: string) => {
        records.delete(key);
    };
    try {
        setActiveUserScope("alice");
        await saveProjectDraft("alice", { ...EMPTY_PROJECT_DRAFT, text: "Alice 的剧本" });
        setActiveUserScope("bob");
        await saveProjectDraft("bob", { ...EMPTY_PROJECT_DRAFT, text: "Bob 的剧本", workflowId: "pipeline-2" });
        await saveProjectDraft("alice", { ...EMPTY_PROJECT_DRAFT, text: "Alice 延迟完成的草稿", whiteModelStoryboardEnabled: true });
        expect((await loadProjectDraft("bob"))?.text).toBe("Bob 的剧本");
        expect((await loadProjectDraft("bob"))?.workflowId).toBe("pipeline-2");
        expect((await loadProjectDraft("alice"))?.workflowId).toBe("pipeline-1");
        expect((await loadProjectDraft("alice"))?.text).toBe("Alice 延迟完成的草稿");
        expect((await loadProjectDraft("alice"))?.whiteModelStoryboardEnabled).toBe(true);
        expect((await loadProjectDraft("bob"))?.whiteModelStoryboardEnabled).toBe(false);
        expect(EMPTY_PROJECT_DRAFT.whiteModelStoryboardEnabled).toBe(false);
        await clearProjectDraft("alice");
        expect(await loadProjectDraft("alice")).toBeUndefined();
        expect((await loadProjectDraft("bob"))?.text).toBe("Bob 的剧本");
        expect([...records.keys()]).toEqual(["yingce:project-create-draft:user:bob"]);
        expect([...preferences.keys()]).toEqual(["yingce:active-user-scope"]);
    } finally {
        Object.assign(localforage, original);
        if (windowDescriptor) Object.defineProperty(globalThis, "window", windowDescriptor);
        else Reflect.deleteProperty(globalThis, "window");
    }
});
