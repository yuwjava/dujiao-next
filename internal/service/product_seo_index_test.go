package service

import (
	"testing"

	"github.com/dujiao-next/internal/models"
)

func TestProductSEONoIndex(t *testing.T) {
	cases := []struct {
		name string
		meta models.JSON
		want bool
	}{
		{"未设置", nil, false},
		{"只有描述", models.JSON{"description": models.JSON{"zh-CN": "x"}}, false},
		{"布尔 true", models.JSON{"noindex": true}, true},
		{"布尔 false", models.JSON{"noindex": false}, false},
		{"字符串 true", models.JSON{"noindex": "true"}, true},
		{"字符串 1", models.JSON{"noindex": "1"}, true},
		{"数字 1", models.JSON{"noindex": float64(1)}, true},
		{"无关字符串", models.JSON{"noindex": "no"}, false},
	}
	for _, c := range cases {
		p := &models.Product{SeoMetaJSON: c.meta}
		if got := productSEONoIndex(p); got != c.want {
			t.Errorf("%s：得到 %v，期望 %v", c.name, got, c.want)
		}
	}
	if productSEONoIndex(nil) {
		t.Errorf("nil 商品应为 false")
	}
}

func TestBuildProductSchemaSKU(t *testing.T) {
	cases := []struct {
		id    uint
		title string
		want  string
	}{
		{12, "教科版小科六上第四单元《计量时间》单元课件", "BK-JK-6S-12"},
		{40, "苏教版小科五下《仿生》单元课件", "BK-SJ-5X-40"},
		{41, "粤教版小科三下《观测天气》单元课件", "BK-YJ-3X-41"},
		{3, "苏教版小科3-6年级知识手册", "BK-SJ-36-3"},
		{29, "教科版小科1-6年级《开学第一课》（起始课）", "BK-JK-16-29"},
		{60, "简易电路仿真实验工具台", "BK-60"},
		{70, "北师版小科六上《光》单元课件", "BK-70"}, // 未登记代码的版本
	}
	for _, c := range cases {
		p := &models.Product{ID: c.id, TitleJSON: models.JSON{"zh-CN": c.title}}
		if got := BuildProductSchemaSKU(p); got != c.want {
			t.Errorf("BuildProductSchemaSKU(%d, %q) = %q；期望 %q", c.id, c.title, got, c.want)
		}
	}
}

// 线上 65 个商品快照按序号模拟 ID，schema sku 必须两两不同
func TestBuildProductSchemaSKUUniqueOverShopCatalog(t *testing.T) {
	seen := map[string]string{}
	for i, title := range shopProductTitlesSnapshot {
		p := &models.Product{ID: uint(i + 1), TitleJSON: models.JSON{"zh-CN": title}}
		sku := BuildProductSchemaSKU(p)
		if prev, dup := seen[sku]; dup {
			t.Errorf("sku 重复：%q 与 %q 都是 %q", prev, title, sku)
		}
		seen[sku] = title
	}
}
