package service

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"

	"github.com/dujiao-next/internal/models"

	"gorm.io/gorm"
)

// ContentReplaceRule 商品详情的一条整段替换规则：Old 按原文逐字匹配，New 为替换后的内容（可为空表示删除）。
// IDs 非空时只作用于这些商品，用于少数商品与通用口径不同的情况；规则按顺序套用，专用规则应写在通用规则前面。
type ContentReplaceRule struct {
	Old string `json:"old"`
	New string `json:"new"`
	IDs []uint `json:"ids,omitempty"`
}

func (r ContentReplaceRule) appliesTo(productID uint) bool {
	if len(r.IDs) == 0 {
		return true
	}
	for _, id := range r.IDs {
		if id == productID {
			return true
		}
	}
	return false
}

// ParseContentReplaceRules 解析规则文件（JSON 数组），拒绝空的 old 和 old == new 的无效规则
func ParseContentReplaceRules(r io.Reader) ([]ContentReplaceRule, error) {
	var rules []ContentReplaceRule
	if err := json.NewDecoder(r).Decode(&rules); err != nil {
		return nil, fmt.Errorf("规则文件不是合法的 JSON 数组: %w", err)
	}
	if len(rules) == 0 {
		return nil, errors.New("规则文件里没有规则")
	}
	for i, rule := range rules {
		if rule.Old == "" {
			return nil, fmt.Errorf("第 %d 条规则的 old 为空", i+1)
		}
		if rule.Old == rule.New {
			return nil, fmt.Errorf("第 %d 条规则的 old 与 new 相同", i+1)
		}
	}
	return rules, nil
}

// applyContentReplaceRules 对多语言详情的每个字符串值依次套用规则。
// 返回新的详情和每条规则命中的次数；没有命中时 changed 为 false，原对象不被修改。
func applyContentReplaceRules(productID uint, content models.JSON, rules []ContentReplaceRule) (models.JSON, []int, bool) {
	hits := make([]int, len(rules))
	if len(content) == 0 {
		return content, hits, false
	}
	out := make(models.JSON, len(content))
	changed := false
	for lang, value := range content {
		text, ok := value.(string)
		if !ok {
			out[lang] = value
			continue
		}
		for i, rule := range rules {
			if !rule.appliesTo(productID) {
				continue
			}
			if n := strings.Count(text, rule.Old); n > 0 {
				hits[i] += n
				text = strings.ReplaceAll(text, rule.Old, rule.New)
				changed = true
			}
		}
		out[lang] = text
	}
	return out, hits, changed
}

// ProductContentReplaceReport 一次批量替换的结果
type ProductContentReplaceReport struct {
	Scanned int   // 扫描的商品数（不含已删除）
	Changed int   // 详情发生变化的商品数
	Hits    []int // 每条规则在全部商品中的命中次数
}

// ReplaceProductContent 按规则批量改写商品详情（content_json），只更新这一列。
// dryRun 为 true 时只统计、不写库；每个改动的商品会向 log 输出一行。
func ReplaceProductContent(db *gorm.DB, rules []ContentReplaceRule, dryRun bool, log io.Writer) (ProductContentReplaceReport, error) {
	report := ProductContentReplaceReport{Hits: make([]int, len(rules))}
	var products []models.Product
	if err := db.Select("id", "slug", "content_json").Order("id").Find(&products).Error; err != nil {
		return report, fmt.Errorf("读取商品失败: %w", err)
	}
	report.Scanned = len(products)
	for _, p := range products {
		next, hits, changed := applyContentReplaceRules(p.ID, p.ContentJSON, rules)
		if !changed {
			continue
		}
		report.Changed++
		parts := make([]string, 0, len(hits))
		for i, n := range hits {
			report.Hits[i] += n
			if n > 0 {
				parts = append(parts, fmt.Sprintf("#%d×%d", i+1, n))
			}
		}
		fmt.Fprintf(log, "商品 %d (%s)：%s\n", p.ID, p.Slug, strings.Join(parts, " "))
		if dryRun {
			continue
		}
		if err := db.Model(&models.Product{}).Where("id = ?", p.ID).Update("content_json", next).Error; err != nil {
			return report, fmt.Errorf("更新商品 %d 失败: %w", p.ID, err)
		}
	}
	return report, nil
}
