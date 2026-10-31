package service

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/dujiao-next/internal/constants"
	"github.com/dujiao-next/internal/models"
	"github.com/dujiao-next/internal/repository"

	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

func newSitemapServiceForTest(t *testing.T) (*SitemapService, *gorm.DB) {
	t.Helper()

	dsn := fmt.Sprintf("file:sitemap_service_%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open sqlite failed: %v", err)
	}
	if err := db.AutoMigrate(
		&models.Category{},
		&models.Product{},
		&models.ProductSKU{},
		&models.Post{},
	); err != nil {
		t.Fatalf("auto migrate failed: %v", err)
	}

	svc := NewSitemapService(
		repository.NewProductRepository(db),
		repository.NewCategoryRepository(db),
		repository.NewPostRepository(db),
	)
	return svc, db
}

func TestSitemapServiceIncludesActiveContent(t *testing.T) {
	svc, db := newSitemapServiceForTest(t)

	activeCategory := models.Category{Slug: "games", NameJSON: models.JSON{"zh-CN": "games"}, IsActive: true}
	inactiveCategory := models.Category{Slug: "hidden", NameJSON: models.JSON{"zh-CN": "hidden"}, IsActive: true}
	if err := db.Create(&activeCategory).Error; err != nil {
		t.Fatalf("create active category: %v", err)
	}
	if err := db.Create(&inactiveCategory).Error; err != nil {
		t.Fatalf("create inactive category: %v", err)
	}
	// GORM 的 default:true tag 会让零值 false 写入时被 DB 默认值覆盖，需显式 Update 才能落到 false
	if err := db.Model(&models.Category{}).Where("id = ?", inactiveCategory.ID).Update("is_active", false).Error; err != nil {
		t.Fatalf("update inactive category: %v", err)
	}

	visibleProduct := models.Product{
		CategoryID:      activeCategory.ID,
		Slug:            "visible-product",
		TitleJSON:       models.JSON{"zh-CN": "p"},
		PriceAmount:     models.NewMoneyFromDecimal(decimal.NewFromInt(10)),
		PurchaseType:    constants.ProductPurchaseMember,
		FulfillmentType: constants.FulfillmentTypeManual,
		IsActive:        true,
	}
	if err := db.Create(&visibleProduct).Error; err != nil {
		t.Fatalf("create product: %v", err)
	}

	hiddenByProductInactive := models.Product{
		CategoryID:      activeCategory.ID,
		Slug:            "draft-product",
		TitleJSON:       models.JSON{"zh-CN": "p"},
		PriceAmount:     models.NewMoneyFromDecimal(decimal.NewFromInt(10)),
		PurchaseType:    constants.ProductPurchaseMember,
		FulfillmentType: constants.FulfillmentTypeManual,
		IsActive:        false,
	}
	if err := db.Create(&hiddenByProductInactive).Error; err != nil {
		t.Fatalf("create draft product: %v", err)
	}

	hiddenByCategoryInactive := models.Product{
		CategoryID:      inactiveCategory.ID,
		Slug:            "in-hidden-category",
		TitleJSON:       models.JSON{"zh-CN": "p"},
		PriceAmount:     models.NewMoneyFromDecimal(decimal.NewFromInt(10)),
		PurchaseType:    constants.ProductPurchaseMember,
		FulfillmentType: constants.FulfillmentTypeManual,
		IsActive:        true,
	}
	if err := db.Create(&hiddenByCategoryInactive).Error; err != nil {
		t.Fatalf("create hidden-category product: %v", err)
	}

	emptyCategory := models.Category{Slug: "coming-soon", NameJSON: models.JSON{"zh-CN": "人教版"}, IsActive: true}
	if err := db.Create(&emptyCategory).Error; err != nil {
		t.Fatalf("create empty category: %v", err)
	}
	noindexOnlyCategory := models.Category{Slug: "templates-only", NameJSON: models.JSON{"zh-CN": "PPT模板"}, IsActive: true}
	if err := db.Create(&noindexOnlyCategory).Error; err != nil {
		t.Fatalf("create noindex-only category: %v", err)
	}

	noindexProduct := models.Product{
		CategoryID:      noindexOnlyCategory.ID,
		Slug:            "noindex-template",
		TitleJSON:       models.JSON{"zh-CN": "p"},
		SeoMetaJSON:     models.JSON{"noindex": true},
		PriceAmount:     models.NewMoneyFromDecimal(decimal.NewFromInt(10)),
		PurchaseType:    constants.ProductPurchaseMember,
		FulfillmentType: constants.FulfillmentTypeManual,
		IsActive:        true,
	}
	if err := db.Create(&noindexProduct).Error; err != nil {
		t.Fatalf("create noindex product: %v", err)
	}

	publishedPost := models.Post{
		Slug:        "hello",
		Type:        constants.PostTypeBlog,
		TitleJSON:   models.JSON{"zh-CN": "hello"},
		IsPublished: true,
	}
	draftPost := models.Post{
		Slug:        "draft",
		Type:        constants.PostTypeBlog,
		TitleJSON:   models.JSON{"zh-CN": "draft"},
		IsPublished: false,
	}
	if err := db.Create(&publishedPost).Error; err != nil {
		t.Fatalf("create published post: %v", err)
	}
	if err := db.Create(&draftPost).Error; err != nil {
		t.Fatalf("create draft post: %v", err)
	}

	xmlStr, err := svc.Generate(context.Background(), "https://example.com")
	if err != nil {
		t.Fatalf("generate failed: %v", err)
	}

	mustContain := []string{
		"<urlset",
		"https://example.com/",
		"https://example.com/products",
		"https://example.com/blog",
		"https://example.com/categories/games",
		"https://example.com/products/visible-product",
		"https://example.com/blog/hello",
	}
	for _, s := range mustContain {
		if !strings.Contains(xmlStr, s) {
			t.Errorf("expected sitemap to contain %q\noutput:\n%s", s, xmlStr)
		}
	}

	mustNotContain := []string{
		"hidden",             // 停用分类
		"draft-product",      // 下架商品
		"in-hidden-category", // 分类停用下的商品
		"/blog/draft",        // 未发布文章
		"noindex-template",   // 后台标记 noindex 的商品
		"coming-soon",        // 没有商品的分类
		"templates-only",     // 只有 noindex 商品的分类
	}
	for _, s := range mustNotContain {
		if strings.Contains(xmlStr, s) {
			t.Errorf("sitemap should not contain %q\noutput:\n%s", s, xmlStr)
		}
	}
}

func TestSitemapServiceGenerateRobotsIncludesSitemapURL(t *testing.T) {
	svc, _ := newSitemapServiceForTest(t)

	body := svc.GenerateRobots("https://example.com")

	mustContain := []string{
		"User-agent: *",
		"Disallow: /admin/",
		"Disallow: /me/",
		"Sitemap: https://example.com/sitemap.xml",
	}
	for _, s := range mustContain {
		if !strings.Contains(body, s) {
			t.Errorf("robots.txt should contain %q\noutput:\n%s", s, body)
		}
	}
}

func TestSitemapServiceGenerateRejectsEmptyBaseURL(t *testing.T) {
	svc, _ := newSitemapServiceForTest(t)
	if _, err := svc.Generate(context.Background(), ""); err == nil {
		t.Fatalf("expected error for empty base url")
	}
}

func TestSitemapServiceStaticLastModUsesLatestContentOrOmitsWhenEmpty(t *testing.T) {
	t.Run("空数据库时省略静态页 lastmod", func(t *testing.T) {
		svc, _ := newSitemapServiceForTest(t)
		xmlStr, err := svc.Generate(context.Background(), "https://empty.example.com")
		if err != nil {
			t.Fatalf("生成空库 sitemap 失败: %v", err)
		}
		if strings.Contains(xmlStr, "<lastmod>") {
			t.Fatalf("空库静态页不应输出虚假的当日 <lastmod>，实际输出:\n%s", xmlStr)
		}
	})

	t.Run("存在商品与文章时静态页取最新真实更新日期", func(t *testing.T) {
		svc, db := newSitemapServiceForTest(t)

		cat := models.Category{
			Slug:      "kejiaoban",
			NameJSON:  models.JSON{"zh-CN": "教科版"},
			IsActive:  true,
			CreatedAt: time.Date(2025, 1, 10, 8, 0, 0, 0, time.UTC),
		}
		if err := db.Create(&cat).Error; err != nil {
			t.Fatalf("创建分类失败: %v", err)
		}

		prodUpdated := time.Date(2025, 6, 15, 12, 0, 0, 0, time.UTC)
		prod := models.Product{
			CategoryID:      cat.ID,
			Slug:            "grade-3-unit-1",
			TitleJSON:       models.JSON{"zh-CN": "三年级上册第一单元课件"},
			PriceAmount:     models.NewMoneyFromDecimal(decimal.NewFromInt(19)),
			PurchaseType:    constants.ProductPurchaseGuest,
			FulfillmentType: constants.FulfillmentTypeAuto,
			IsActive:        true,
		}
		if err := db.Create(&prod).Error; err != nil {
			t.Fatalf("创建商品失败: %v", err)
		}
		if err := db.Model(&models.Product{}).Where("id = ?", prod.ID).UpdateColumn("updated_at", prodUpdated).Error; err != nil {
			t.Fatalf("更新商品时间失败: %v", err)
		}

		postPub := time.Date(2025, 8, 20, 9, 30, 0, 0, time.UTC)
		post := models.Post{
			Slug:        "science-guide",
			Type:        constants.PostTypeBlog,
			TitleJSON:   models.JSON{"zh-CN": "小学科学实验备课指南"},
			IsPublished: true,
			PublishedAt: &postPub,
		}
		if err := db.Create(&post).Error; err != nil {
			t.Fatalf("创建文章失败: %v", err)
		}

		xmlStr, err := svc.Generate(context.Background(), "https://dated.example.com")
		if err != nil {
			t.Fatalf("生成 sitemap 失败: %v", err)
		}

		// 最新日期应为文章发布日 2025-08-20，而非当前系统日期
		expectedStaticSnippet := "<loc>https://dated.example.com/</loc>\n    <lastmod>2025-08-20</lastmod>"
		if !strings.Contains(xmlStr, expectedStaticSnippet) {
			t.Fatalf("静态首页应使用最新内容更新日期 2025-08-20，实际输出:\n%s", xmlStr)
		}
		today := time.Now().UTC().Format("2006-01-02")
		if today != "2025-08-20" && strings.Contains(xmlStr, "<lastmod>"+today+"</lastmod>") {
			t.Fatalf("不应无脑写入当天日期 %s，实际输出:\n%s", today, xmlStr)
		}
	})
}
