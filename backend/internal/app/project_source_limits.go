package app

import "unicode"

const projectSourceMaxChars = 150_000

func projectSourceChars(source string) int {
	count := 0
	for _, character := range chapterPlainText(source) {
		if !unicode.IsSpace(character) {
			count++
		}
	}
	return count
}

func validateProjectSourceSize(source string) error {
	if projectSourceChars(source) > projectSourceMaxChars {
		return projectSectionError(ReasonProjectSourceInvalid, "正文最多支持 15 万字，请分章导入或缩短正文")
	}
	return nil
}
