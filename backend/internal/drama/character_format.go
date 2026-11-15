package drama

import "regexp"

// Only explicit frame annotations are rewritten; character proportions and
// numeric appearance details in existing asset definitions remain untouched.
var fourPanelFrameAnnotation = regexp.MustCompile(`(?i)(?:landscape|portrait|aspect\s*ratio\s*[:：]?|画幅(?:比例)?\s*[:：]?|画面比例\s*[:：]?|竖版|横版|横屏|竖屏)\s*[0-9]+\s*[:：]\s*[0-9]+`)

func FourPanelPortraitPrompt(prompt string) string {
	return fourPanelFrameAnnotation.ReplaceAllString(prompt, "竖版9:16")
}
