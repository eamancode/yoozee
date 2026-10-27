import type { DramaLine, DramaSegment, DramaSegmentView, DramaSubShot } from "@/services/api/drama";

import { firstSentence, type SegmentDurationRule } from "./chapter-segments";

/**
 * 一次段编辑要写回的内容：segments 交给 PUT /drama/segments；拆段、合段时 order 是本章全部段号的新顺序，
 * inherit 是「段号 → 从哪几段继承资产引用」（人物、场景、道具、本场色卡，后端去重），
 * removedShotIds 是会被删掉的段（连同生成结果）；selectShotId 是保存后应选中的段。
 */
export type SegmentEdit = { segments: DramaSegment[]; order?: string[]; inherit?: Record<string, string[]>; removedShotIds: string[]; selectShotId?: string };

/** 段内每个镜头的时长：到下一个镜头的开始秒；最后一个镜头算到段尾（含按渠道档位补齐的部分）。 */
export function subShotSeconds(segment: DramaSegment) {
    return segment.shots.map((shot, index) => roundSeconds((segment.shots[index + 1]?.at ?? segment.dur) - shot.at));
}

/** 在第 shotIndex 个镜头前拆开：前半留原段号，后半是紧跟在后面的新段（S02 → S02b）。 */
export function splitSegment(list: DramaSegmentView[], index: number, shotIndex: number, rule: SegmentDurationRule): SegmentEdit {
    const view = editable(list, index);
    const segment = view.spec;
    if (shotIndex <= 0 || shotIndex >= segment.shots.length) throw new Error("只能在段内两个镜头之间拆开");
    const seconds = subShotSeconds(segment);
    const id = nextSegmentId(
        segment.id,
        list.map((item) => item.spec.id),
    );
    const head = rebuild(withoutTail(segment), segment.shots.slice(0, shotIndex), seconds.slice(0, shotIndex), rule);
    const tail = rebuild({ ...withoutHead(segment), id, title: firstSentence(segment.shots[shotIndex].text) || id }, segment.shots.slice(shotIndex), seconds.slice(shotIndex), rule);
    const order = list.map((item) => item.spec.id);
    order.splice(index + 1, 0, id);
    return { segments: [head, tail], order, inherit: { [id]: [segment.id] }, removedShotIds: [] };
}

/** 第 index 段吸收紧跟的下一段：下一段删除，参考（段里的和镜头绑的资产引用）取两段并集。 */
export function mergeWithNext(list: DramaSegmentView[], index: number, rule: SegmentDurationRule): SegmentEdit {
    const first = editable(list, index).spec;
    const second = editable(list, index + 1).spec;
    const merged = rebuild(
        {
            ...first,
            last_frame: second.last_frame,
            hook: second.hook,
            refs: unionBy([...(first.refs || []), ...(second.refs || [])], (ref) => ref.file),
            voices: unionBy([...(first.voices || []), ...(second.voices || [])], (voice) => voice.character),
            sound: joinDistinct(first.sound, second.sound),
            rules: joinDistinct(first.rules, second.rules),
        },
        [...first.shots, ...second.shots],
        [...subShotSeconds(first), ...subShotSeconds(second)],
        rule,
    );
    return { segments: [merged], order: list.map((item) => item.spec.id).filter((id) => id !== second.id), inherit: { [first.id]: [second.id] }, removedShotIds: [list[index + 1].shotId], selectShotId: list[index].shotId };
}

/** 把首镜头移到上一段末尾，或把尾镜头移到下一段开头。两段都留着，只是段内镜头变了。 */
export function moveEdgeShot(list: DramaSegmentView[], index: number, direction: "prev" | "next", rule: SegmentDurationRule): SegmentEdit {
    const source = editable(list, index).spec;
    const targetIndex = direction === "prev" ? index - 1 : index + 1;
    const target = editable(list, targetIndex).spec;
    if (source.shots.length < 2) throw new Error(`${source.id} 只剩一个镜头，直接和${direction === "prev" ? "上" : "下"}一段合并`);
    const sourceSeconds = subShotSeconds(source);
    const targetSeconds = subShotSeconds(target);
    const refs = unionBy([...(target.refs || []), ...(source.refs || [])], (ref) => ref.file);
    if (direction === "prev") {
        const moved = rebuild({ ...target, refs, last_frame: undefined, hook: undefined }, [...target.shots, source.shots[0]], [...targetSeconds, sourceSeconds[0]], rule);
        const rest = rebuild(withoutHead(source), source.shots.slice(1), sourceSeconds.slice(1), rule);
        return { segments: [moved, rest], inherit: { [target.id]: [source.id] }, removedShotIds: [] };
    }
    const last = source.shots.length - 1;
    const rest = rebuild(withoutTail(source), source.shots.slice(0, last), sourceSeconds.slice(0, last), rule);
    const moved = rebuild(withoutHead({ ...target, refs }), [source.shots[last], ...target.shots], [sourceSeconds[last], ...targetSeconds], rule);
    return { segments: [rest, moved], inherit: { [target.id]: [source.id] }, removedShotIds: [] };
}

/** 改段内一个镜头的画面、台词和时长。只改文字时段长不动；改了时长才按渠道档位重算段长。 */
export function editSubShot(list: DramaSegmentView[], index: number, shotIndex: number, patch: { text: string; lines: DramaLine[]; seconds: number }, rule: SegmentDurationRule): SegmentEdit {
    const segment = editable(list, index).spec;
    const original = segment.shots[shotIndex];
    if (!original) throw new Error("镜头不存在");
    const text = patch.text.trim();
    if (!text) throw new Error("镜头画面不能为空");
    const lines = patch.lines.map((line) => {
        const kept = original.lines?.find((item) => item.speaker === line.speaker && item.text === line.text);
        return kept?.delivery ? { ...line, delivery: kept.delivery } : line;
    });
    const shot: DramaSubShot = { ...original, text, lines: lines.length ? lines : undefined };
    const shots = segment.shots.map((item, position) => (position === shotIndex ? shot : item));
    const seconds = subShotSeconds(segment);
    const nextSeconds = roundSeconds(patch.seconds);
    if (!(nextSeconds > 0)) throw new Error("镜头时长要大于 0 秒");
    if (nextSeconds === seconds[shotIndex]) return { segments: [{ ...segment, shots }], removedShotIds: [] };
    seconds[shotIndex] = nextSeconds;
    return { segments: [rebuild(segment, shots, seconds, rule)], removedShotIds: [] };
}

/** 段没有人物/场景参考（纯文本，或锁人物却一个参考都没挂）：人物在段与段之间只靠文字保持一致。有首帧的锁首帧段不算。 */
/** 段编辑保存失败时卡片上的说明：哪些段的改动还没写进分段表、为什么，改动留着可以重试。 */
export function failedSegmentEditHint(edit: SegmentEdit, error: string) {
    const ids = edit.segments.map((segment) => segment.id);
    return `保存失败：${error}。${ids.join("、")} 的改动还没写进分段表，已保留在这里，可以重试或放弃。`;
}

export function segmentLacksReferences(segment: DramaSegment) {
    if (segment.mode === "lock_first_frame") return !segment.first_frame;
    return segment.mode === "text_only" || !segment.refs?.length;
}

/**
 * 镜头资产绑定变了，段的参考跟着变：生成视频只认段里的参考，只绑镜头资产、不改段，编译出来仍然没有参考。
 * 加参考时纯文本段改成锁人物；去掉最后一个参考（也没有音色参考）时锁人物段改回纯文本。
 * 锁首帧段只认首帧（多数模型首帧与参考互斥），不往里加参考。没有变化时返回 undefined。
 */
export function withSegmentRef(segment: DramaSegment, change: { add: { file: string; role: string; note?: string } } | { removeFiles: string[] }): DramaSegment | undefined {
    const refs = segment.refs || [];
    if ("add" in change) {
        if (segment.mode === "lock_first_frame" || !change.add.file || refs.some((ref) => ref.file === change.add.file)) return undefined;
        return { ...segment, refs: [...refs, change.add], mode: segment.mode === "text_only" ? "lock_identity" : segment.mode };
    }
    const kept = refs.filter((ref) => !change.removeFiles.includes(ref.file));
    if (kept.length === refs.length) return undefined;
    const mode = segment.mode === "lock_identity" && !kept.length && !segment.voices?.length ? "text_only" : segment.mode;
    return { ...segment, refs: kept.length ? kept : undefined, mode };
}

function editable(list: DramaSegmentView[], index: number) {
    const view = list[index];
    if (!view) throw new Error("没有相邻的段");
    if (view.derived) throw new Error(`${view.spec.id} 是旧镜头，还没有分段 JSON，不能拆合`);
    return view;
}

function rebuild(base: DramaSegment, shots: DramaSubShot[], seconds: number[], rule: SegmentDurationRule): DramaSegment {
    let at = 0;
    const timed = shots.map((shot, index) => {
        const next = { ...shot, at: roundSeconds(at) };
        at += seconds[index];
        return next;
    });
    const content = roundSeconds(at);
    if (rule.problem) throw new Error(rule.problem);
    const dur = rule.submitSeconds(content);
    if (dur === undefined) throw new Error(`${base.id} 内容 ${content} 秒，超过一段上限 ${rule.maxSeconds} 秒（${rule.label}）`);
    const segment: DramaSegment = { ...base, shots: timed, dur };
    // 锁首帧的段拆掉首镜头后就没有首帧了，改成按参考锁身份或纯文本。
    if (segment.mode === "lock_first_frame" && !segment.first_frame) segment.mode = segment.refs?.length || segment.voices?.length ? "lock_identity" : "text_only";
    if (segment.mode === "text_only" && segment.refs?.length) segment.mode = "lock_identity";
    return segment;
}

// 值为 undefined 的键在 JSON 里会被省略，写回后端时就是删掉这一项。
function withoutTail(segment: DramaSegment): DramaSegment {
    return { ...segment, last_frame: undefined, hook: undefined };
}

function withoutHead(segment: DramaSegment): DramaSegment {
    return { ...segment, first_frame: undefined };
}

/** S02 → S02b → S02c；段号规则见后端 segmentIDPattern（S + 两三位数字 + 可选一个小写字母）。 */
export function nextSegmentId(id: string, taken: string[]) {
    const match = /^(S\d{2,3})([a-z]?)$/.exec(id);
    if (!match) throw new Error(`段号 ${id} 无效，不能拆段`);
    const used = new Set(taken);
    for (let code = match[2] ? match[2].charCodeAt(0) + 1 : "b".charCodeAt(0); code <= "z".charCodeAt(0); code += 1) {
        const candidate = `${match[1]}${String.fromCharCode(code)}`;
        if (!used.has(candidate)) return candidate;
    }
    throw new Error(`${match[1]} 已经拆到 ${match[1]}z，先合并几段再拆`);
}

function unionBy<T>(items: T[], key: (item: T) => string) {
    const seen = new Set<string>();
    const out = items.filter((item) => {
        const value = key(item);
        if (seen.has(value)) return false;
        seen.add(value);
        return true;
    });
    return out.length ? out : undefined;
}

function joinDistinct(...values: Array<string | undefined>) {
    const parts = Array.from(new Set(values.map((value) => value?.trim() || "").filter(Boolean)));
    return parts.length ? parts.join("；") : undefined;
}

function roundSeconds(value: number) {
    return Math.round(value * 100) / 100;
}
