package service

import (
	"html"
	"strings"

	"github.com/dujiao-next/internal/constants"
	"github.com/dujiao-next/internal/models"

	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
)

// productCoursewareTestedDevices 课件已实测可用的教学大屏（2026-10-31 确认希沃；未实测的设备不要写入）
const productCoursewareTestedDevices = "希沃"

// 商品正文 SSR 输出允许保留的语义标签（全部去掉属性）
var productContentAllowedTags = map[atom.Atom]bool{
	atom.H2: true, atom.H3: true, atom.H4: true,
	atom.P: true, atom.Br: true,
	atom.Ul: true, atom.Ol: true, atom.Li: true,
	atom.Strong: true, atom.B: true, atom.Em: true, atom.I: true,
	atom.Blockquote: true,
}

// 连同内容一起丢弃的标签；其余未列入白名单的标签只去掉标签本身、保留其中文字
var productContentDroppedTags = map[atom.Atom]bool{
	atom.Script: true, atom.Style: true, atom.Iframe: true, atom.Object: true,
	atom.Embed: true, atom.Noscript: true, atom.Template: true, atom.Form: true,
	atom.Input: true, atom.Button: true, atom.Textarea: true, atom.Select: true,
	atom.Svg: true, atom.Math: true, atom.Head: true, atom.Title: true,
	atom.Img: true, atom.Video: true, atom.Audio: true,
}

// sanitizeProductContentHTML 把后台富文本正文转成只含语义标签的安全 HTML：
// 白名单标签保留（h1 降级为 h2，保证整页只有一个 h1），属性全部去掉，文字重新转义。
func sanitizeProductContentHTML(raw string) string {
	trimmed := strings.TrimSpace(raw)
	if trimmed == "" {
		return ""
	}
	context := &xhtml.Node{Type: xhtml.ElementNode, Data: "body", DataAtom: atom.Body}
	nodes, err := xhtml.ParseFragment(strings.NewReader(trimmed), context)
	if err != nil {
		return ""
	}
	var b strings.Builder
	for _, n := range nodes {
		writeSanitizedNode(&b, n)
	}
	return strings.TrimSpace(b.String())
}

func writeSanitizedNode(b *strings.Builder, n *xhtml.Node) {
	switch n.Type {
	case xhtml.TextNode:
		b.WriteString(html.EscapeString(n.Data))
		return
	case xhtml.ElementNode:
	default:
		return
	}

	if productContentDroppedTags[n.DataAtom] {
		return
	}
	tag := n.DataAtom
	if tag == atom.H1 {
		tag = atom.H2
	}
	if !productContentAllowedTags[tag] {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			writeSanitizedNode(b, c)
		}
		return
	}
	if tag == atom.Br {
		b.WriteString("<br>")
		return
	}
	name := tag.String()
	b.WriteString("<" + name + ">")
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		writeSanitizedNode(b, c)
	}
	b.WriteString("</" + name + ">")
}

// buildProductResourceInfoHTML 资源包信息块：只列从商品名与发货类型能确定的真实字段；商品名无法解析时返回空
func buildProductResourceInfoHTML(productTitle string, fulfillmentType string) string {
	p, ok := parseProductTitle(productTitle)
	if !ok {
		return ""
	}

	type field struct{ name, value string }
	fields := []field{
		{"教材版本", p.Version},
		{"年级册次", p.gradeLabel()},
	}
	switch p.Kind {
	case productKindUnitCourseware:
		fields = append(fields,
			field{"单元", p.unitLabel() + "《" + p.Lesson + "》"},
			field{"资源类型", "单元课件"},
			field{"交付格式", "PPT课件、教学设计"},
			field{"适配大屏设备", productCoursewareTestedDevices},
		)
	case productKindHandbook:
		fields = append(fields,
			field{"资源类型", "知识手册"},
			field{"交付格式", "Word文档"},
		)
	case productKindStarterLesson:
		fields = append(fields,
			field{"资源类型", "《开学第一课》起始课"},
			field{"交付格式", "PPT课件、教学设计、逐字稿"},
			field{"适配大屏设备", productCoursewareTestedDevices},
		)
	}
	switch fulfillmentType {
	case constants.FulfillmentTypeAuto:
		fields = append(fields, field{"发货方式", "付款后自动发货"})
	case constants.FulfillmentTypeManual:
		fields = append(fields, field{"发货方式", "人工发货"})
	}

	var b strings.Builder
	b.WriteString(`<section class="resource-info"><h2>资源包信息</h2><dl>`)
	for _, f := range fields {
		b.WriteString("<dt>")
		b.WriteString(html.EscapeString(f.name))
		b.WriteString("</dt><dd>")
		b.WriteString(html.EscapeString(f.value))
		b.WriteString("</dd>")
	}
	b.WriteString(`</dl></section>`)
	return b.String()
}

// buildProductSpecListHTML 可选规格清单（单元课件即课时目录），按前台展示顺序输出已启用 SKU 的规格名
func buildProductSpecListHTML(skus []models.ProductSKU) string {
	seen := map[string]bool{}
	var items []string
	for _, sku := range skus {
		if !sku.IsActive {
			continue
		}
		name := strings.TrimSpace(localizedJSONText(sku.SpecValuesJSON, ""))
		if name == "" || seen[name] {
			continue
		}
		seen[name] = true
		items = append(items, name)
	}
	if len(items) == 0 {
		return ""
	}

	var b strings.Builder
	b.WriteString(`<section class="specs"><h2>可选规格</h2><ul>`)
	for _, item := range items {
		b.WriteString("<li>")
		b.WriteString(html.EscapeString(item))
		b.WriteString("</li>")
	}
	b.WriteString(`</ul></section>`)
	return b.String()
}
