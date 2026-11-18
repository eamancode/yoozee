import type { SegmentAsset } from "@/pages/projects/workbench/detail/shot-card-layout";
import { projectAssetChoices, projectAssetReferenceImage, projectAssetVoiceSampleResourceId, shotAssetReferences } from "@/pages/projects/workbench/detail/workflow-shot-references";
import type { DramaSegmentView } from "@/services/api/project-drama";
import type { ProjectAsset, ProjectUnitWorkspace, ShotAssetReference } from "@/services/api/projects";

export type UnitBinding = SegmentAsset & { asset: ProjectAsset; versionId: string; version?: number; reference?: ShotAssetReference; sampleResourceId: string };

export function isUnitBindingAsset(asset: ProjectAsset) {
    return asset.category === "character" || asset.category === "environment" || asset.category === "prop";
}

export function boundAssetsOf(workspace: ProjectUnitWorkspace | undefined, segment: DramaSegmentView): UnitBinding[] {
    if (!workspace) return [];
    const byVersion = new Map(projectAssetChoices(workspace.assets).map((asset) => [asset.primaryVersionId, asset]));
    const linked = shotAssetReferences(workspace, segment.shotId);
    const ordinary = new Map(linked.filter((reference) => reference.role === "reference").map((reference) => [reference.assetVersionId, reference]));
    const rows = new Map<string, UnitBinding>();
    function add(versionId: string, reference?: ShotAssetReference) {
        const role = reference?.role || "reference";
        const key = `${role}:${versionId}`;
        const asset = reference?.asset || byVersion.get(versionId);
        if (!asset || rows.has(key)) return;
        const kind = role === "style" || asset.category === "palette" ? "palette" : asset.category === "character" ? "character" : asset.category === "prop" ? "prop" : asset.category === "environment" ? "scene" : "other";
        rows.set(key, {
            id: key,
            asset,
            title: asset.title,
            kind,
            versionId,
            versionIds: [versionId],
            version: reference?.referencedVersion?.version ?? (versionId === asset.character?.versionId ? asset.character.version : undefined),
            preview: projectAssetReferenceImage(asset, reference)?.url || "",
            code: asset.palette?.code,
            sampleResourceId: projectAssetVoiceSampleResourceId(asset, reference),
            reference,
        });
    }
    for (const ref of segment.spec.refs || []) add(ref.file, ordinary.get(ref.file));
    for (const reference of linked) add(reference.assetVersionId, reference);
    return Array.from(rows.values());
}

export function availableBindingAssets(assets: ProjectAsset[], bindings: UnitBinding[], replace?: UnitBinding) {
    const bound = new Set(bindings.filter((item) => !item.reference || item.reference.role === "reference").map((item) => item.versionId));
    return projectAssetChoices(assets).filter((asset) => {
        const version = asset.confirmedImageVersionId || asset.primaryVersionId;
        return isUnitBindingAsset(asset) && version && !bound.has(version) && (!replace || asset.category === replace.asset.category);
    });
}
