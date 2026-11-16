package drama

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode"
)

const ContractSSSScriptAdapt = "sss.script@1"
const SSSScriptEpisodeInstruction = "每集按顺序输出 JSON 集头与定稿剧本、## 第一部分　本集设计、## 第二部分　台词定稿表。按手册/04、05 完成设计与台词手术，标注保留的原台词逐字保留。定稿台词必须出现在本集正文，表中记录处理和原句。"

func SplitSSSScriptEpisode(text string) (script, design, lines string, err error) {
	rows := strings.Split(text, "\n")
	first, second := -1, -1
	for i, row := range rows {
		switch strings.Join(strings.Fields(row), " ") {
		case "## 第一部分 本集设计":
			if first >= 0 || second >= 0 {
				return "", "", "", errors.New("本集设计标题重复或顺序错误")
			}
			first = i
		case "## 第二部分 台词定稿表":
			if first < 0 || second >= 0 {
				return "", "", "", errors.New("台词定稿表标题重复或顺序错误")
			}
			second = i
		}
	}
	if first < 1 || second <= first {
		return "", "", "", errors.New("按剧本、本集设计、台词定稿表的顺序输出三块")
	}
	script = strings.TrimSpace(strings.Join(rows[:first], "\n"))
	design = strings.TrimSpace(strings.Join(rows[first+1:second], "\n"))
	lines = strings.TrimSpace(strings.Join(rows[second+1:], "\n"))
	if script == "" || design == "" || lines == "" {
		return "", "", "", errors.New("剧本、本集设计、台词定稿表均须有正文")
	}
	return script, design, lines, nil
}

func normalizedDialogue(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case unicode.IsSpace(r):
			return -1
		case r == '“' || r == '”':
			return '"'
		case r == '‘' || r == '’':
			return '\''
		}
		return r
	}, s)
}

func sssDialogueText(line string) (string, bool) {
	line = strings.TrimSpace(line)
	if _, ok := ScriptLineSpeaker(line); !ok {
		return "", false
	}
	// 冒号前可以有含标点的表演提示。末尾对白保留原引号，供逐字检查。
	match := scriptLinePattern.FindStringSubmatchIndex(line)
	start := max(match[3], match[5])
	return strings.TrimSpace(strings.TrimLeft(line[start:], "：:")), true
}

// 只识别台词行上的显式保留标记，避免把对白里普通的「保留」当作控制指令。
func SSSLockedLines(sources []ScriptAdaptSource) map[string]map[int]string {
	var locked map[string]map[int]string
	for _, source := range sources {
		for i, line := range ScriptAdaptParagraphs(source.Text) {
			for _, mark := range []string{"【保留】", "[保留]", "（保留）", "(保留)"} {
				if !strings.Contains(line, mark) {
					continue
				}
				clean := strings.TrimSpace(strings.Replace(line, mark, "", 1))
				value, ok := sssDialogueText(clean)
				if !ok {
					continue
				}
				if value == "" {
					continue
				}
				if locked == nil {
					locked = map[string]map[int]string{}
				}
				if locked[source.UnitID] == nil {
					locked[source.UnitID] = map[int]string{}
				}
				locked[source.UnitID][i+1] = value
				break
			}
		}
	}
	return locked
}

func checkSSSLockedLines(turn TurnContext, episode AdaptEpisode, script string) error {
	if len(turn.LockedLines) == 0 {
		return nil
	}
	var spoken []string
	for _, line := range strings.Split(script, "\n") {
		if value, ok := sssDialogueText(line); ok {
			spoken = append(spoken, value)
		}
	}
	for id, lines := range turn.LockedLines {
		for n, value := range lines {
			inScope := id == turn.SourceEpisode || slices.ContainsFunc(episode.Sources, func(src AdaptSource) bool { return src.UnitID == id && n >= src.From && n <= src.To })
			if inScope && !slices.ContainsFunc(spoken, func(line string) bool { return strings.Contains(line, value) }) {
				return fmt.Errorf("标记保留的第 %d 段台词未逐字保留：%s", n, value)
			}
		}
	}
	return nil
}

func SSSLineTableCheck(table, script string) error {
	var dialogue []string
	for _, line := range strings.Split(script, "\n") {
		if value, ok := sssDialogueText(line); ok {
			dialogue = append(dialogue, normalizedDialogue(value))
		}
	}
	column, rows := -1, 0
	var missing []string
	for _, line := range strings.Split(table, "\n") {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "|") {
			continue
		}
		cells := strings.Split(strings.Trim(line, "|"), "|")
		for i := range cells {
			cells[i] = strings.TrimSpace(cells[i])
		}
		if column < 0 {
			column = slices.Index(cells, "定稿台词")
			continue
		}
		if strings.Trim(strings.Join(cells, ""), ":- ") == "" {
			continue
		}
		if column >= len(cells) {
			return errors.New("台词定稿表缺少定稿台词列")
		}
		rows++
		value := cells[column]
		switch value {
		case "", "删", "（删）", "(删)":
			continue
		}
		needle := normalizedDialogue(value)
		if !slices.ContainsFunc(dialogue, func(line string) bool { return strings.Contains(line, needle) }) && len(missing) < 3 {
			missing = append(missing, value)
		}
	}
	if column < 0 {
		return errors.New("台词定稿表的表头须含「定稿台词」列")
	}
	if len(missing) > 0 {
		return fmt.Errorf("定稿台词未在本集剧本中出现：%s", strings.Join(missing, "、"))
	}
	if rows == 0 && len(dialogue) > 0 {
		return errors.New("台词定稿表缺少数据行")
	}
	return nil
}
