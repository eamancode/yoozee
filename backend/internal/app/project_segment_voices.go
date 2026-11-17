package app

import (
	"encoding/json"

	"yingce/backend/internal/drama"
	"yingce/backend/internal/model"
)

// A shared sample has one tag but a separate entry for each speaking binding.
type DramaVoiceBinding struct {
	VersionID        string `json:"versionId"`
	SampleResourceID string `json:"sampleResourceId"`
	Tag              string `json:"tag"`
}

func resolveSegmentVoices(seg *drama.Segment, project *model.Project, derived bool, lookup func(string) dramaRefAsset) map[string]int {
	var bible struct {
		Characters []struct {
			Name   string `json:"name"`
			Speaks *bool  `json:"speaks"`
		} `json:"characters"`
	}
	_ = json.Unmarshal([]byte(project.BibleJSON), &bible)
	silent := map[string]bool{}
	for _, ch := range bible.Characters {
		if ch.Speaks != nil && !*ch.Speaks {
			silent[ch.Name] = true
		}
	}
	samples := map[string]drama.BoundVoiceSample{}
	for _, ref := range seg.Refs {
		info := lookup(ref.File)
		if info.sampleID == "" || (derived && !info.hasImage) || info.asset.Category != model.AssetCategoryCharacter {
			continue
		}
		character := info.asset.Title
		if v := info.asset.Variant; v != nil && v.ParentTitle != "" {
			character = v.ParentTitle
		}
		speaker := firstNonEmpty(ref.Speaker, character)
		samples[ref.File] = drama.BoundVoiceSample{
			Character: character, Speaker: speaker, ResourceID: info.sampleID,
			Speaks: !silent[character] && !silent[speaker] && (info.canSpeak == nil || *info.canSpeak),
		}
	}
	voices, sent := drama.SegmentVoices(*seg, samples, derived)
	seg.Voices = voices
	return sent
}

func segmentVoiceBindings(refs []drama.Ref, sent map[string]int, lookup func(string) dramaRefAsset, m drama.Model) []DramaVoiceBinding {
	var bindings []DramaVoiceBinding
	for _, ref := range refs {
		if index, ok := sent[ref.File]; ok {
			info := lookup(ref.File)
			bindings = append(bindings, DramaVoiceBinding{VersionID: info.versionID, SampleResourceID: info.sampleID, Tag: drama.ReferenceAudioTag(m, index)})
		}
	}
	return bindings
}
