package service

import (
	"fmt"
	"strings"

	"github.com/dujiao-next/internal/models"
)

// productSEONoIndex 商品级“不让搜索引擎收录”开关：后台 seo_meta.noindex 为 true 时，
// 商品页输出 noindex 并从 sitemap 中移除（商品仍可正常访问和购买）
func productSEONoIndex(p *models.Product) bool {
	if p == nil || p.SeoMetaJSON == nil {
		return false
	}
	switch v := p.SeoMetaJSON["noindex"].(type) {
	case bool:
		return v
	case string:
		s := strings.ToLower(strings.TrimSpace(v))
		return s == "true" || s == "1"
	case float64:
		return v == 1
	}
	return false
}

// 教材版本在 schema sku 中的代码
var productVersionSKUCodes = map[string]string{
	"教科版": "JK",
	"苏教版": "SJ",
	"粤教版": "YJ",
	"大象版": "DX",
	"人教版": "RJ",
}

var productGradeDigits = map[string]string{
	"一": "1", "二": "2", "三": "3", "四": "4", "五": "5", "六": "6",
}

// BuildProductSchemaSKU 生成 Product schema 的全局唯一 sku：BK-{版本}-{年级册次}-{商品ID}，
// 如 BK-JK-6S-12（教科版六上）、BK-SJ-36-3（苏教版3-6年级）；商品名无法解析时为 BK-{商品ID}。
// 只用于 SEO 输出，不改数据库里的 sku_code（上游映射与下单校验仍按原编码）。
func BuildProductSchemaSKU(p *models.Product) string {
	if p == nil {
		return ""
	}
	fallback := fmt.Sprintf("BK-%d", p.ID)

	parsed, ok := parseProductTitle(localizedJSONText(p.TitleJSON, ""))
	if !ok {
		return fallback
	}
	version, ok := productVersionSKUCodes[parsed.Version]
	if !ok {
		return fallback
	}

	var gradeTerm string
	switch parsed.Term {
	case "上":
		gradeTerm = productGradeDigits[parsed.Grade] + "S"
	case "下":
		gradeTerm = productGradeDigits[parsed.Grade] + "X"
	default:
		gradeTerm = strings.ReplaceAll(parsed.Grade, "-", "")
	}
	if gradeTerm == "" || gradeTerm == "S" || gradeTerm == "X" {
		return fallback
	}
	return fmt.Sprintf("BK-%s-%s-%d", version, gradeTerm, p.ID)
}
