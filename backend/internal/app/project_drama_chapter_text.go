package app

import (
	"regexp"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"yingce/backend/internal/drama"
)

// chapterEditorText 是章节编辑器（TipTap）getText() 对保存的 HTML 给出的纯文本：块之间空一行（"\n\n"），
// <br> 是换行，文本里的连续空白按 ProseMirror 的规则压成一个空格、块首块尾不留空格，最后去掉首尾空白。
// 生成分段原先在浏览器里取 editor.getText()，搬到服务端后正文必须逐字一致，镜头里的台词才能对上原文。
func chapterEditorText(source string) string {
	root, err := html.Parse(strings.NewReader(source))
	if err != nil {
		return drama.JSTrim(chapterPlainText(source))
	}
	w := &editorTextWriter{}
	w.walk(root, false)
	return drama.JSTrim(w.out.String())
}

var editorWhitespace = regexp.MustCompile(`[ \t\r\n\f]+`)

// editorBlocks 是 StarterKit 里的块节点；每个块（第一个除外）前面加一个空行。
var editorBlocks = map[atom.Atom]bool{
	atom.P: true, atom.H1: true, atom.H2: true, atom.H3: true, atom.H4: true, atom.H5: true, atom.H6: true,
	atom.Blockquote: true, atom.Ul: true, atom.Ol: true, atom.Li: true, atom.Pre: true, atom.Hr: true, atom.Div: true,
}

type editorTextWriter struct {
	out strings.Builder
	// started 表示已经出现过块（再出现的块前加分隔）。
	started bool
	// block 是当前块里已写的文字（判断块首、块尾空白）。
	block strings.Builder
	// afterBreak 表示上一个行内节点是 <br>。
	afterBreak bool
}

func (w *editorTextWriter) walk(n *html.Node, pre bool) {
	switch n.Type {
	case html.TextNode:
		w.text(n.Data, pre)
		return
	case html.ElementNode:
		if n.DataAtom == atom.Br {
			w.block.WriteString("\n")
			w.out.WriteString("\n")
			w.afterBreak = true
			return
		}
		if editorBlocks[n.DataAtom] {
			w.closeBlock()
			if w.started {
				w.out.WriteString("\n\n")
			}
			w.started = true
			for c := n.FirstChild; c != nil; c = c.NextSibling {
				w.walk(c, pre || n.DataAtom == atom.Pre)
			}
			w.closeBlock()
			return
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		w.walk(c, pre)
	}
}

func (w *editorTextWriter) text(value string, pre bool) {
	if !pre {
		value = editorWhitespace.ReplaceAllString(value, " ")
		current := w.block.String()
		if strings.HasPrefix(value, " ") && (current == "" || w.afterBreak || strings.HasSuffix(current, " ")) {
			value = value[1:]
		}
	}
	if value == "" {
		return
	}
	w.afterBreak = false
	w.block.WriteString(value)
	w.out.WriteString(value)
}

// closeBlock 去掉块尾的空格（ProseMirror 解析时不保留）。
func (w *editorTextWriter) closeBlock() {
	current := w.block.String()
	if trimmed := strings.TrimRight(current, " "); len(trimmed) < len(current) {
		full := w.out.String()
		w.out.Reset()
		w.out.WriteString(full[:len(full)-(len(current)-len(trimmed))])
	}
	w.block.Reset()
	w.afterBreak = false
}
