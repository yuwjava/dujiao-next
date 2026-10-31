package service

import (
	"testing"

	"github.com/dujiao-next/internal/models"
)

func TestCategoryHeadingAndTitle(t *testing.T) {
	cases := []struct {
		name        string
		wantHeading string
		wantTitle   string
	}{
		{"教科版", "教科版小学科学课件", "教科版小学科学课件｜按年级单元选课 - 备课箱"},
		{"人教版", "人教版小学科学课件", "人教版小学科学课件｜按年级单元选课 - 备课箱"},
		{"小学科学", "小学科学备课资源", "小学科学备课资源｜按教材版本选课 - 备课箱"},
		{"小工具", "小工具", "小工具｜备课箱商城"},
		{"PPT模板", "PPT模板", "PPT模板｜备课箱商城"},
		{"仿真实验工具", "仿真实验工具", "仿真实验工具｜备课箱商城"},
	}
	for _, c := range cases {
		if got := categoryHeading(c.name); got != c.wantHeading {
			t.Errorf("categoryHeading(%q) = %q；期望 %q", c.name, got, c.wantHeading)
		}
		if got := categoryPageTitle(c.name); got != c.wantTitle {
			t.Errorf("categoryPageTitle(%q) = %q；期望 %q", c.name, got, c.wantTitle)
		}
	}
}

func TestCategoryIDsWithIndexableProducts(t *testing.T) {
	// 1 小学科学 ─┬─ 2 教科版（有商品）
	//            └─ 6 大象版（空）
	// 7 PPT模板（只有 noindex 商品）
	// 8 小工具（有商品）
	categories := []models.Category{
		{ID: 1}, {ID: 2, ParentID: 1}, {ID: 6, ParentID: 1}, {ID: 7}, {ID: 8},
	}
	products := []models.Product{
		{CategoryID: 2},
		{CategoryID: 7, SeoMetaJSON: models.JSON{"noindex": true}},
		{CategoryID: 8},
	}
	got := categoryIDsWithIndexableProducts(categories, products)
	want := map[uint]bool{1: true, 2: true, 8: true}
	if len(got) != len(want) {
		t.Fatalf("得到 %v，期望 %v", got, want)
	}
	for id := range want {
		if !got[id] {
			t.Errorf("分类 %d 应被标记为有可收录商品，得到 %v", id, got)
		}
	}

	// 父链成环的异常数据不应死循环
	loop := []models.Category{{ID: 1, ParentID: 2}, {ID: 2, ParentID: 1}}
	if r := categoryIDsWithIndexableProducts(loop, []models.Product{{CategoryID: 1}}); !r[1] || !r[2] {
		t.Errorf("成环数据应标记链上分类，得到 %v", r)
	}
}
