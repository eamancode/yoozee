import { useUserStore } from "@/stores/use-user-store";

/** 画布技能入口遵循当前账号的技能展示设置。 */
export function hasSkillLibraryAccess(state = useUserStore.getState()) {
    return state.hydrated && Boolean(state.user?.id) && state.features.skillContentVisible;
}

export function useSkillLibraryAccess() {
    return useUserStore(hasSkillLibraryAccess);
}

export function skillLibraryUserId() {
    const state = useUserStore.getState();
    return hasSkillLibraryAccess(state) ? state.user!.id : null;
}

let permissionRevision = 0;
useUserStore.subscribe((state, previous) => {
    if (state.hydrated !== previous.hydrated || state.user?.id !== previous.user?.id || state.user?.role !== previous.user?.role || state.features.skillContentVisible !== previous.features.skillContentVisible) permissionRevision += 1;
});

export function captureSkillLibraryAccess() {
    const userId = skillLibraryUserId();
    return userId ? { userId, revision: permissionRevision } : null;
}

export function isSkillLibraryAccessCurrent(access: ReturnType<typeof captureSkillLibraryAccess>) {
    return Boolean(access && access.userId === skillLibraryUserId() && access.revision === permissionRevision);
}
