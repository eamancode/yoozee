package drama

// 镜头表检查结果挂到分镜卡上（M8 卡 H）：带问题装段以后，分镜制作按镜头标出问题，台词覆盖类问题给快捷修复。

// ShotIssue 是一条检查问题挂在哪几个镜头上。
type ShotIssue struct {
	CheckItem
	// Scene 是问题所在的场（「重新生成问题场」按它挑场）；整章级、认不出场的为空。
	Scene string `json:"scene,omitempty"`
	// Shots 是问题挂在哪几镜上（镜头表 id）：规则指向的镜头；台词没落镜时挂在本场它前一句（没有就后一句）台词所在的镜头，
	// 本场一句都没落镜时挂在本场第一镜；台词落在多镜时是这几镜。都找不到时为空，只在侧栏列出。
	Shots []string `json:"shots,omitempty"`
	// Fix 是快捷修复：add_line 把 LineID 补进这一镜，keep_line 只保留在这一镜（其余镜头去掉）。其他问题为空，只显示文字。
	Fix string `json:"fix,omitempty"`
}

const (
	ShotIssueFixAddLine  = "add_line"
	ShotIssueFixKeepLine = "keep_line"
)

// ShotIssues 把检查结果里 level 级的问题（level 为空时全部）挂到镜头上。shotlist 是检查的那张镜头表。
func ShotIssues(report CheckReport, shotlist Shotlist, script *Script, level string) []ShotIssue {
	sceneOf := map[string]string{}
	sceneShots := map[string][]string{}
	used := map[string][]string{}
	for _, sh := range shotlist.Shots {
		if _, ok := sceneOf[sh.ID]; !ok {
			sceneOf[sh.ID] = sh.Scene
			sceneShots[sh.Scene] = append(sceneShots[sh.Scene], sh.ID)
		}
		for _, ln := range sh.Lines {
			if ln.LineID != "" {
				used[ln.LineID] = append(used[ln.LineID], sh.ID)
			}
		}
	}
	lineScene := map[string]string{}
	sceneLines := map[string][]string{}
	if script != nil {
		for _, sc := range script.Scenes {
			for _, b := range sc.Beats {
				if b.isLine() && lineScene[b.ID] == "" {
					lineScene[b.ID] = sc.ID
					sceneLines[sc.ID] = append(sceneLines[sc.ID], b.ID)
				}
			}
		}
	}
	// anchor 是没落镜的台词挂在哪一镜：本场前一句落了镜的台词所在的最后一镜，没有就后一句的第一镜，再没有就本场第一镜。
	anchor := func(lineID string) []string {
		scene := lineScene[lineID]
		order := sceneLines[scene]
		at := -1
		for i, id := range order {
			if id == lineID {
				at = i
			}
		}
		for i := at - 1; i >= 0; i-- {
			if where := used[order[i]]; len(where) > 0 {
				return []string{where[len(where)-1]}
			}
		}
		for i := at + 1; i >= 1 && i < len(order); i++ {
			if where := used[order[i]]; len(where) > 0 {
				return []string{where[0]}
			}
		}
		if shots := sceneShots[scene]; len(shots) > 0 {
			return []string{shots[0]}
		}
		return nil
	}
	out := []ShotIssue{}
	for _, item := range report.Items {
		if level != "" && item.Level != level {
			continue
		}
		issue := ShotIssue{CheckItem: item}
		where := used[item.LineID]
		switch {
		case item.Rule == "R-SHOT-001" && item.LineID != "" && lineScene[item.LineID] != "" && len(where) == 0:
			issue.Scene, issue.Shots, issue.Fix = lineScene[item.LineID], anchor(item.LineID), ShotIssueFixAddLine
		case item.Rule == "R-SHOT-001" && item.LineID != "" && lineScene[item.LineID] != "" && len(where) > 1 && item.ShotID == where[1]:
			issue.Scene, issue.Shots, issue.Fix = sceneOf[where[0]], dedupeStrings(where), ShotIssueFixKeepLine
		case item.ShotID != "":
			issue.Scene, issue.Shots = sceneOf[item.ShotID], []string{item.ShotID}
		}
		out = append(out, issue)
	}
	return out
}
