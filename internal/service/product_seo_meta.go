package service

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/dujiao-next/internal/models"
)

// productReturnPolicyText 商品退换口径（写入 Product schema 的 MerchantReturnPolicy）
const productReturnPolicyText = "虚拟商品发货后不退款；文件损坏、错发或缺件可补发。"

// productResourceKind 商品资源类型，决定兜底 SEO 描述只写该类型真实的交付物
type productResourceKind int

const (
	productKindUnknown        productResourceKind = iota
	productKindUnitCourseware                     // 单元课件
	productKindHandbook                           // 知识手册（Word 文档）
	productKindStarterLesson                      // 开学第一课（起始课）
)

// parsedProductTitle 从商品展示名中解析出的教材定位信息
type parsedProductTitle struct {
	Kind    productResourceKind
	Version string // 教科版 / 苏教版 / 粤教版 ...
	Grade   string // 单册商品为 一..六；跨年级商品为 "3-6" 这类区间
	Term    string // 上 / 下；跨年级商品为空
	Unit    string // 第X单元 中的 X，标题未写单元时为空
	Lesson  string // 书名号内课名
}

var (
	// 例：教科版小科六上第四单元《计量时间》单元课件、苏教版小科五下《仿生》单元课件
	unitCoursewareTitleRe = regexp.MustCompile(`^(\p{Han}{2,3}版)小科([一二三四五六])([上下])(?:第([一二三四五六七八九十]+)单元)?《([^》]+)》单元课件$`)
	// 例：教科版小科3-6年级知识手册
	handbookTitleRe = regexp.MustCompile(`^(\p{Han}{2,3}版)小科(\d-\d)年级知识手册$`)
	// 例：苏教版小科1-6年级《开学第一课》（起始课）
	starterLessonTitleRe = regexp.MustCompile(`^(\p{Han}{2,3}版)小科(\d-\d)年级《开学第一课》（起始课）$`)
)

// parseProductTitle 解析商品展示名；无法识别的格式返回 false
func parseProductTitle(title string) (parsedProductTitle, bool) {
	t := strings.TrimSpace(title)
	if m := unitCoursewareTitleRe.FindStringSubmatch(t); m != nil {
		return parsedProductTitle{Kind: productKindUnitCourseware, Version: m[1], Grade: m[2], Term: m[3], Unit: m[4], Lesson: m[5]}, true
	}
	if m := handbookTitleRe.FindStringSubmatch(t); m != nil {
		return parsedProductTitle{Kind: productKindHandbook, Version: m[1], Grade: m[2]}, true
	}
	if m := starterLessonTitleRe.FindStringSubmatch(t); m != nil {
		return parsedProductTitle{Kind: productKindStarterLesson, Version: m[1], Grade: m[2], Lesson: "开学第一课"}, true
	}
	return parsedProductTitle{}, false
}

// gradeLabel 年级册次：单册为“六年级上册”，跨年级为“3-6年级”
func (p parsedProductTitle) gradeLabel() string {
	if p.Term == "" {
		return p.Grade + "年级"
	}
	return p.Grade + "年级" + p.Term + "册"
}

// unitLabel 单元：有单元号时为“第四单元”，否则为空
func (p parsedProductTitle) unitLabel() string {
	if p.Unit == "" {
		return ""
	}
	return "第" + p.Unit + "单元"
}

// ResolveProductPageSEO 商品详情页最终的 <title> 与 meta description。
// 服务端 HTML 壳与公开商品详情接口共用，保证前台 JS 渲染后与服务端输出一致。
func ResolveProductPageSEO(p *models.Product) (title string, description string) {
	if p == nil {
		return "", ""
	}
	prodTitle := localizedJSONText(p.TitleJSON, p.Slug)
	shortDesc := strings.TrimSpace(localizedJSONText(p.DescriptionJSON, ""))
	title = BuildProductPageTitle(extractLocalizedFieldFromMap(p.SeoMetaJSON, "title"), prodTitle)
	description = BuildStructuredProductDescription(extractLocalizedFieldFromMap(p.SeoMetaJSON, "description"), prodTitle, shortDesc)
	return title, description
}

// BuildProductPageTitle 生成商品详情页 <title>：后台 seo_meta.title 优先，否则按标题解析结果套模板
func BuildProductPageTitle(rawSeoTitle string, productTitle string) string {
	seoTitle := strings.TrimSpace(rawSeoTitle)
	if seoTitle != "" {
		if strings.Contains(seoTitle, "备课箱") {
			return seoTitle
		}
		return seoTitle + "｜备课箱"
	}

	name := strings.TrimSpace(productTitle)
	p, ok := parseProductTitle(name)
	if !ok {
		return name + "｜备课箱小学科学"
	}
	prefix := p.Version + "小学科学" + p.gradeLabel()
	switch p.Kind {
	case productKindUnitCourseware:
		if unit := p.unitLabel(); unit != "" {
			prefix += " " + unit
		}
		return fmt.Sprintf("%s《%s》单元课件｜备课箱", prefix, p.Lesson)
	case productKindHandbook:
		return prefix + "知识手册｜备课箱"
	case productKindStarterLesson:
		return prefix + "《开学第一课》起始课｜备课箱"
	}
	return name + "｜备课箱小学科学"
}

// BuildStructuredProductDescription 生成商品详情 meta description：
// 后台 seo_meta.description 有效（长度 > 15 且不等于商品名）时直接采用；
// 否则按资源类型套模板，只写该类型真实的交付物；无法解析的商品退回商品短描述或通用句。
func BuildStructuredProductDescription(rawSeoDesc string, productTitle string, shortDesc string) string {
	trimmedSeo := strings.TrimSpace(rawSeoDesc)
	trimmedTitle := strings.TrimSpace(productTitle)
	if isUsableProductDescription(trimmedSeo, trimmedTitle) {
		return trimmedSeo
	}

	p, ok := parseProductTitle(trimmedTitle)
	if ok {
		prefix := p.Version + "小学科学" + p.gradeLabel()
		switch p.Kind {
		case productKindUnitCourseware:
			return fmt.Sprintf("%s%s《%s》单元课件，含课件PPT与教学设计，部分版本另含练习题、逐字稿与讲解视频。AI辅助生成，教研组三审三校；扫码购买，自动发货。", prefix, p.unitLabel(), p.Lesson)
		case productKindHandbook:
			return fmt.Sprintf("%s知识手册，Word文档，按单元汇总知识点，适合课前预习与单元复习，可直接打印。扫码购买，自动发货。", prefix)
		case productKindStarterLesson:
			return fmt.Sprintf("%s《开学第一课》起始课课件，含课件PPT、教学设计与逐字稿，用于各年级开学第一节科学课。扫码购买，自动发货。", prefix)
		}
	}

	trimmedShort := strings.TrimSpace(shortDesc)
	if isUsableProductDescription(trimmedShort, trimmedTitle) {
		return trimmedShort
	}
	return fmt.Sprintf("%s，备课箱小学科学备课商城在售资源。", trimmedTitle)
}

func isUsableProductDescription(desc string, title string) bool {
	return desc != "" && utf8.RuneCountInString(desc) > 15 && desc != title
}
