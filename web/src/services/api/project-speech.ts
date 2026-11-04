import { http } from "./request";

export type SpeechEntry = { scene: string; paragraph: number; paragraph_end?: number; speaker: string; kind: "dialogue" | "voiceover" | "narration"; text: string };
export type UnitSpeechLedger = { ready: boolean; ledger: null | { sourceHash: string; skillVersion: string; entries: SpeechEntry[] } };

export function getUnitSpeechLedger(projectId: string, unitId: string, signal?: AbortSignal) {
    return http.get<UnitSpeechLedger>(`/projects/${encodeURIComponent(projectId)}/units/${encodeURIComponent(unitId)}/drama/speech-ledger`, { signal });
}
