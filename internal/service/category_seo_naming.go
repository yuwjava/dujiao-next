package service

import "strings"

// productsCenterHeading 商品中心的 H1 / 面包屑名称（商城同时有课件、知识手册与工具，不写“课件”“全版本”）
const productsCenterHeading = "小学科学备课资源中心"

// categoryHeading 分类页 H1 / 面包屑名称，按分类性质区分，避免给“小工具”“PPT模板”冠上“小学科学课件”：
//   - 教材版本（名称以“版”结尾）：教科版小学科学课件
//   - 学科父分类（名称含“小学科学”）：小学科学备课资源
//   - 其他：分类名本身
//
// 规则须与 user/src/utils/seo.ts 的 buildCategoryHeading 保持一致。
func categoryHeading(catName string) string {
	name := strings.TrimSpace(catName)
	switch {
	case strings.HasSuffix(name, "版"):
		return name + "小学科学课件"
	case strings.Contains(name, "小学科学"):
		return name + "备课资源"
	default:
		return name
	}
}

// withSiteSuffix 页面标题追加站点后缀“ - 备课箱”；标题里已含“备课箱”时不再追加，
// 与 user/src/composables/usePageSeo.ts 的 fullTitle 规则一致，避免“备课箱 | xxx - 备课箱”这类重复
func withSiteSuffix(title string) string {
	t := strings.TrimSpace(title)
	if strings.Contains(t, "备课箱") {
		return t
	}
	return t + " - 备课箱"
}

// categoryPageTitle 分类页完整 <title>
func categoryPageTitle(catName string) string {
	name := strings.TrimSpace(catName)
	switch {
	case strings.HasSuffix(name, "版"):
		return categoryHeading(name) + "｜按年级单元选课 - 备课箱"
	case strings.Contains(name, "小学科学"):
		return categoryHeading(name) + "｜按教材版本选课 - 备课箱"
	default:
		return name + "｜备课箱商城"
	}
}
