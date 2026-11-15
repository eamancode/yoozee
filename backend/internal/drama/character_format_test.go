package drama

import (
	"strings"
	"testing"
)

func TestFourPanelPortraitPromptPreservesAppearanceAndLayout(t *testing.T) {
	for _, frame := range []string{"竖版2:3", "landscape 16:9", "Portrait 2:3", "aspect ratio: 1:1", "画幅：4:3", "横屏16：9"} {
		original := frame + " four-panel，身体比例2:3；上排38%，下排62%；黑发，身高1.8米；道具宽高2:3。"
		got := FourPanelPortraitPrompt(original)
		if strings.Contains(got, frame) || !strings.Contains(got, "竖版9:16 four-panel") || !strings.Contains(got, "身体比例2:3") || !strings.Contains(got, "道具宽高2:3") || !strings.Contains(got, "上排38%，下排62%") {
			t.Fatalf("unexpected frame normalization: %q", got)
		}
		if FourPanelPortraitPrompt(got) != got {
			t.Fatal("repeated compilation changed format")
		}
	}
}
