package service

import (
	"fmt"
	"io"
	"strings"
	"testing"
	"time"

	"github.com/dujiao-next/internal/models"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

func TestParseContentReplaceRulesRejectsInvalid(t *testing.T) {
	cases := map[string]string{
		"非数组":        `{"old":"a","new":"b"}`,
		"空数组":        `[]`,
		"old 为空":     `[{"old":"","new":"b"}]`,
		"old 等于 new": `[{"old":"a","new":"a"}]`,
	}
	for name, raw := range cases {
		if _, err := ParseContentReplaceRules(strings.NewReader(raw)); err == nil {
			t.Errorf("%s：应当报错", name)
		}
	}
	rules, err := ParseContentReplaceRules(strings.NewReader(`[{"old":"a","new":""}]`))
	if err != nil || len(rules) != 1 {
		t.Fatalf("new 为空（删除整段）应当允许: %v", err)
	}
}

func TestApplyContentReplaceRulesCountsEveryLanguage(t *testing.T) {
	rules := []ContentReplaceRule{{Old: "旧A", New: "新A"}, {Old: "旧B", New: ""}, {Old: "不存在", New: "x"}}
	content := models.JSON{"zh-CN": "旧A 旧A 旧B", "en-US": "旧A", "meta": 1}

	next, hits, changed := applyContentReplaceRules(1, content, rules)
	if !changed {
		t.Fatalf("应当有改动")
	}
	if got := fmt.Sprint(hits); got != "[3 1 0]" {
		t.Fatalf("命中次数 = %s，want [3 1 0]", got)
	}
	if next["zh-CN"] != "新A 新A " || next["en-US"] != "新A" || next["meta"] != 1 {
		t.Fatalf("替换结果不对: %#v", next)
	}
	if content["zh-CN"] != "旧A 旧A 旧B" {
		t.Fatalf("不应修改传入的原对象")
	}

	if _, _, changed := applyContentReplaceRules(1, models.JSON{"zh-CN": "无关内容"}, rules); changed {
		t.Fatalf("没有命中时不应报告改动")
	}
}

func TestApplyContentReplaceRulesScopedByIDs(t *testing.T) {
	// 专用规则在前：商品 2 整段删除，其他商品走通用改写
	rules := []ContentReplaceRule{
		{Old: "<li>不可编辑</li>", New: "", IDs: []uint{2}},
		{Old: "<li>不可编辑</li>", New: "<li>图片版不可编辑</li>"},
	}
	scoped, _, _ := applyContentReplaceRules(2, models.JSON{"zh-CN": "<li>不可编辑</li>"}, rules)
	if scoped["zh-CN"] != "" {
		t.Fatalf("商品 2 应命中专用规则并删除，实际 %q", scoped["zh-CN"])
	}
	general, hits, _ := applyContentReplaceRules(3, models.JSON{"zh-CN": "<li>不可编辑</li>"}, rules)
	if general["zh-CN"] != "<li>图片版不可编辑</li>" || hits[0] != 0 || hits[1] != 1 {
		t.Fatalf("商品 3 应只命中通用规则: %q %v", general["zh-CN"], hits)
	}
}

func TestReplaceProductContentDryRunAndApply(t *testing.T) {
	dsn := fmt.Sprintf("file:product_content_replace_%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite failed: %v", err)
	}
	if err := db.AutoMigrate(&models.Product{}); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}
	seed := []models.Product{
		{Slug: "a", TitleJSON: models.JSON{"zh-CN": "A"}, ContentJSON: models.JSON{"zh-CN": "<p>虚拟商品不可退换</p>"}},
		{Slug: "b", TitleJSON: models.JSON{"zh-CN": "B"}, ContentJSON: models.JSON{"zh-CN": "<p>其他</p>"}},
	}
	if err := db.Create(&seed).Error; err != nil {
		t.Fatalf("seed failed: %v", err)
	}
	rules := []ContentReplaceRule{{Old: "<p>虚拟商品不可退换</p>", New: "<p>文件损坏、错发或缺件可补发</p>"}}

	read := func(slug string) string {
		var p models.Product
		if err := db.Where("slug = ?", slug).First(&p).Error; err != nil {
			t.Fatalf("读取 %s 失败: %v", slug, err)
		}
		return fmt.Sprint(p.ContentJSON["zh-CN"])
	}

	report, err := ReplaceProductContent(db, rules, true, io.Discard)
	if err != nil {
		t.Fatalf("dry-run 失败: %v", err)
	}
	if report.Scanned != 2 || report.Changed != 1 || report.Hits[0] != 1 {
		t.Fatalf("dry-run 统计不对: %+v", report)
	}
	if read("a") != "<p>虚拟商品不可退换</p>" {
		t.Fatalf("dry-run 不应写库")
	}

	if _, err := ReplaceProductContent(db, rules, false, io.Discard); err != nil {
		t.Fatalf("执行失败: %v", err)
	}
	if read("a") != "<p>文件损坏、错发或缺件可补发</p>" || read("b") != "<p>其他</p>" {
		t.Fatalf("执行结果不对: a=%q b=%q", read("a"), read("b"))
	}
	var title models.Product
	_ = db.Where("slug = ?", "a").First(&title).Error
	if title.TitleJSON["zh-CN"] != "A" {
		t.Fatalf("不应改动详情以外的列")
	}

	// 再跑一次：已经替换过，不再有改动（可重复执行）
	again, err := ReplaceProductContent(db, rules, false, io.Discard)
	if err != nil || again.Changed != 0 {
		t.Fatalf("重复执行应当没有改动: %+v %v", again, err)
	}
}
