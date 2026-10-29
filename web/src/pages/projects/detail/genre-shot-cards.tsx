import type { ReactNode } from "react";
import { Box, Image as ImageIcon, MapPin, Palette, UserRound } from "lucide-react";

import type { CardField, SectionLayoutModel, SegmentAsset, ShotCardModel } from "./shot-card-layout";

/**
 * 分镜卡按戏类版式的显示件（M8 卡 G）：小节头、每张卡的正文、卡下的绑定资产、小节尾和技能原文。
 * 数据由 shot-card-layout 算好，这里只排版；颜色、字号、圆角都走 workflow.css 里 .genre-* 的 token。
 */

/** 小节头：技能原生的小节标题，加小节级原样字段（7.3 的要素提取与站位、即梦的小节头）。 */
export function SectionLayoutHead({ layout }: { layout: SectionLayoutModel }) {
    if (layout.legacy) return null;
    return (
        <div className={`genre-section-head is-${layout.genre}`}>
            {layout.title ? <div className="genre-section-title">{layout.title}</div> : null}
            {layout.head.length ? <FieldList fields={layout.head} className="genre-section-fields" /> : null}
        </div>
    );
}

/** 小节尾：7.3 的结束站位与继承点、即梦的小节约束与连续性总结（收起），和这一节生成时的技能原文（收起）。 */
export function SectionLayoutTail({ layout }: { layout: SectionLayoutModel }) {
    if (layout.legacy) return null;
    const tailOpen = layout.genre === "wenxi15";
    return (
        <div className="genre-section-tail">
            {layout.tail.length ? (
                tailOpen ? (
                    <FieldList fields={layout.tail} className="genre-section-fields" />
                ) : (
                    <details className="genre-disclosure">
                        <summary>小节尾：{layout.tail.map((field) => field.label).join("、")}</summary>
                        <FieldList fields={layout.tail} className="genre-section-fields" />
                    </details>
                )
            ) : null}
            {layout.raw ? (
                <details className="genre-disclosure">
                    <summary>看技能原文</summary>
                    <p className="genre-raw-note">这一节生成时技能的原样输出（含 BGM、粘贴提示词等不进镜头表的内容）。逐张编辑改的是镜头表，原文不跟着改。</p>
                    <pre className="genre-raw thin-scrollbar">{layout.raw}</pre>
                </details>
            ) : null}
        </div>
    );
}

/** 一张卡的正文：按戏类排成技能原本的样子。actions 是右上角的编辑按钮，children 是卡上的检查问题，assets 是卡下的绑定资产。 */
export function GenreShotCard({ layout, card, highlighted, actions, assets, children }: { layout: SectionLayoutModel; card: ShotCardModel; highlighted?: boolean; actions?: ReactNode; assets?: SegmentAsset[]; children?: ReactNode }) {
    return (
        <article className={`genre-card is-${layout.genre}${highlighted ? " has-issues" : ""}`}>
            <header className="genre-card-head">
                <span className="genre-card-code">{card.code}</span>
                <span className="genre-card-span">{card.span}</span>
                {card.camera ? <span className="genre-card-camera">{card.camera}</span> : null}
                {card.tags.length && layout.genre === "wuxi" ? (
                    <span className="genre-card-tags">
                        {card.tags.map((tag) => (
                            <TagChip key={tag.label} label={tag.label} value={tag.value} level={tag.level} />
                        ))}
                    </span>
                ) : null}
                {actions ? <span className="genre-card-actions">{actions}</span> : null}
            </header>
            {card.body ? <p className="genre-card-body">{card.body}</p> : null}
            {card.tags.length && layout.genre !== "wuxi" ? (
                <div className="genre-card-tags">
                    {card.tags.map((tag) => (
                        <TagChip key={tag.label} label={tag.label} value={tag.value} />
                    ))}
                </div>
            ) : null}
            {card.fields.length ? <FieldList fields={card.fields} className="genre-card-fields" /> : null}
            {card.lines.length ? (
                <ul className="genre-card-lines" aria-label={`${card.code} 台词`}>
                    {card.lines.map((line, index) => (
                        <li key={`${line.speaker}-${index}`}>
                            <span className="genre-card-speaker">{line.speaker || "旁白"}</span>
                            {layout.genre === "wenxi15" ? `（台词${line.note?.includes("画外") ? "·画外" : ""}）：“${line.text}”` : `：${line.text}`}
                            {layout.genre !== "wenxi15" && line.note ? <span className="genre-card-delivery">{line.note}</span> : null}
                        </li>
                    ))}
                </ul>
            ) : null}
            {children}
            {assets?.length ? <CardAssets assets={assets} /> : null}
        </article>
    );
}

function FieldList({ fields, className }: { fields: CardField[]; className: string }) {
    return (
        <dl className={className}>
            {fields.map((field, index) => (
                <div key={`${field.label}-${index}`} className="contents">
                    <dt>{field.label}</dt>
                    <dd>{field.value}</dd>
                </div>
            ))}
        </dl>
    );
}

function TagChip({ label, value, level }: { label: string; value: string; level?: number }) {
    return (
        <span className={`genre-tag${level ? ` is-level-${Math.min(4, level)}` : ""}`} title={`${label}：${value}`}>
            <em>{label}</em>
            {value}
        </span>
    );
}

const ASSET_ICONS: Record<SegmentAsset["kind"], typeof Box> = { character: UserRound, prop: Box, scene: MapPin, palette: Palette, other: ImageIcon };
const ASSET_KIND_LABELS: Record<SegmentAsset["kind"], string> = { character: "角色", prop: "道具", scene: "场景", palette: "本场色卡", other: "资产" };

/** 卡下的绑定资产：缩略图 + 名称，没有图时用分类图标占位。 */
export function CardAssets({ assets }: { assets: SegmentAsset[] }) {
    return (
        <ul className="genre-card-assets" aria-label="这张卡绑定的资产">
            {assets.map((asset) => {
                const Icon = ASSET_ICONS[asset.kind];
                const name = asset.kind === "palette" ? `${asset.code || ""} 色卡`.trim() : asset.title;
                return (
                    <li key={asset.id} className={`genre-asset is-${asset.kind}`} title={`${ASSET_KIND_LABELS[asset.kind]}：${asset.title}`}>
                        <span className="genre-asset-thumb">{asset.preview ? <img src={asset.preview} alt="" loading="lazy" /> : <Icon aria-hidden />}</span>
                        <span className="genre-asset-name">{name}</span>
                    </li>
                );
            })}
        </ul>
    );
}
