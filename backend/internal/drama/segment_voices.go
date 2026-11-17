package drama

import (
	"regexp"
	"slices"
	"strings"
)

// BoundVoiceSample comes from the selected character version and its own voice binding.
type BoundVoiceSample struct {
	Character  string
	Speaker    string
	ResourceID string
	Speaks     bool
}

// SegmentVoices returns the ordered upload list and the one-based audio slot of
// each participating visual version. A shared sample can serve multiple speakers.
func SegmentVoices(seg Segment, samples map[string]BoundVoiceSample, derived bool) ([]Voice, map[string]int) {
	var voices []Voice
	byFile := make(map[string]int)
	byVersion := make(map[string]int)
	for _, ref := range seg.Refs {
		sample := samples[ref.File]
		if ref.Role != "identity" || sample.ResourceID == "" || sample.Character == "" {
			continue
		}
		speaker := firstNonBlank(ref.Speaker, sample.Speaker, sample.Character)
		if !derived && (!sample.Speaks || !boundCharacterHasLine(seg, ref.File, speaker)) {
			continue
		}
		n := byFile[sample.ResourceID]
		if n == 0 {
			voices = append(voices, Voice{Character: speaker, File: sample.ResourceID})
			n = len(voices)
			byFile[sample.ResourceID] = n
		} else if !slices.Contains(strings.Split(voices[n-1].Character, "、"), speaker) {
			voices[n-1].Character += "、" + speaker
		}
		byVersion[ref.File] = n
	}
	return voices, byVersion
}

func boundCharacterHasLine(seg Segment, version, speaker string) bool {
	// A version attached only to a silent subshot must not borrow a line from
	// another appearance. Director cards and unit-level additions bind the unit.
	perShot := slices.ContainsFunc(seg.Shots, func(sh SubShot) bool { return slices.Contains(sh.Refs, version) })
	director := seg.Verbatim != ""
	hasDialogueColumn := false
	var speech *regexp.Regexp
	var plainLine *regexp.Regexp
	if director {
		// The director templates allow a parenthesized delivery before the quote.
		// Start at a clause boundary so a shorter name cannot match a different character.
		name := `\s*@?` + regexp.QuoteMeta(speaker) + `\s*(?:[（(][^）)\n]*[）)]\s*)?`
		speech = regexp.MustCompile(`(?:^|[\n；;。，,！？!?])` + name + `(?:说[：:]?\s*|[：:]\s*)?[“"「]([^”"」\n]+)[”"」]`)
		plainLine = regexp.MustCompile(`(?:^|[\n；;])` + name + `(?:说\s*)?[：:]\s*([^“"「\n；;]+)`)
	}
	for _, sh := range seg.Shots {
		if perShot && !slices.Contains(sh.Refs, version) {
			continue
		}
		for _, line := range sh.Lines {
			if line.Speaker == speaker && strings.TrimSpace(line.Text) != "" {
				return true
			}
		}
		if !director || strings.TrimSpace(sh.Sound) == "" {
			continue
		}
		hasDialogueColumn = true
		if speech.MatchString(sh.Sound) {
			return true
		}
		// Some ontology tables point to a line written out in the verbatim prompt.
		if strings.TrimSpace(sh.Sound) == speaker+"台词" && speech.MatchString(seg.Verbatim) {
			return true
		}
		for _, match := range plainLine.FindAllStringSubmatch(sh.Sound, -1) {
			text := strings.Trim(match[1], " \t。—-")
			if text != "" && !strings.HasPrefix(text, "无台词") && !strings.HasPrefix(text, "没有台词") && !slices.Contains([]string{"无", "沉默", "静默"}, text) {
				return true
			}
		}
	}
	// Older ontology segments stored only the action column. A direct attributed
	// quote in their verbatim text still identifies a speaker; a voice description cannot.
	return director && !perShot && !hasDialogueColumn && speech.MatchString(seg.Verbatim)
}
