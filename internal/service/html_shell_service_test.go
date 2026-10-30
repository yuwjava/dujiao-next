package service

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/dujiao-next/internal/constants"
	"github.com/dujiao-next/internal/models"
	"github.com/dujiao-next/internal/repository"

	"github.com/glebarez/sqlite"
	"github.com/shopspring/decimal"
	"gorm.io/gorm"
)

func setupHTMLShellTestEnv(t *testing.T, templateURL string) (*HTMLShellService, *gorm.DB) {
	t.Helper()

	dsn := fmt.Sprintf("file:html_shell_%d?mode=memory&cache=shared", time.Now().UnixNano())
	db, err := gorm.Open(sqlite.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("打开测试 SQLite 失败: %v", err)
	}
	if err := db.AutoMigrate(
		&models.Category{},
		&models.Product{},
		&models.ProductSKU{},
		&models.Post{},
		&models.Setting{},
	); err != nil {
		t.Fatalf("自动迁移测试表失败: %v", err)
	}

	productRepo := repository.NewProductRepository(db)
	categoryRepo := repository.NewCategoryRepository(db)
	postRepo := repository.NewPostRepository(db)
	settingRepo := repository.NewSettingRepository(db)
	settingSvc := NewSettingService(settingRepo)

	if templateURL == "" {
		// 默认指向不可达本地关闭端口，验证内置最小壳零报错兜底
		templateURL = "http://127.0.0.1:1/index.html"
	}

	svc := NewHTMLShellService(HTMLShellOptions{
		ProductRepo:    productRepo,
		CategoryRepo:   categoryRepo,
		PostRepo:       postRepo,
		SettingService: settingSvc,
		TemplateURL:    templateURL,
		TemplateTTL:    30 * time.Second,
		HTTPClient:     &http.Client{Timeout: 200 * time.Millisecond},
	})
	return svc, db
}

func TestHTMLShellServiceHomeAndTemplateETagCache(t *testing.T) {
	var hitCount int32
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hitCount, 1)
		if r.Header.Get("If-None-Match") == `"v1-etag"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"v1-etag"`)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><html lang="en"><head><meta charset="UTF-8" /><title></title><script type="module" crossorigin src="/assets/index-abc123.js"></script></head><body><div id="app"></div></body></html>`))
	}))
	defer mockServer.Close()

	svc, db := setupHTMLShellTestEnv(t, mockServer.URL+"/index.html")
	svc.templateTTL = 10 * time.Millisecond

	// 模拟后台残留泛化的标题“备课箱”与“小初高全学科”描述
	settingRepo := repository.NewSettingRepository(db)
	_, err := settingRepo.Upsert(constants.SettingKeySiteConfig, map[string]interface{}{
		"seo": map[string]interface{}{
			"title":       map[string]interface{}{"zh-CN": "备课箱"},
			"description": map[string]interface{}{"zh-CN": "覆盖小初高全学科教学资源"},
		},
	})
	if err != nil {
		t.Fatalf("写入测试站点设置失败: %v", err)
	}

	cat := models.Category{
		Slug:     "kejiaoban",
		NameJSON: models.JSON{"zh-CN": "教科版"},
		IsActive: true,
	}
	if err := db.Create(&cat).Error; err != nil {
		t.Fatalf("创建分类失败: %v", err)
	}

	prod := models.Product{
		CategoryID:      cat.ID,
		Slug:            "jk-3a-01",
		TitleJSON:       models.JSON{"zh-CN": "三年级上册《水到哪里去了》课件"},
		PriceAmount:     models.NewMoneyFromDecimal(decimal.NewFromFloat(9.9)),
		PurchaseType:    constants.ProductPurchaseGuest,
		FulfillmentType: constants.FulfillmentTypeAuto,
		IsActive:        true,
	}
	if err := db.Create(&prod).Error; err != nil {
		t.Fatalf("创建商品失败: %v", err)
	}

	res, err := svc.Render(context.Background(), "/", "https://shop.beikevip.site")
	if err != nil {
		t.Fatalf("渲染首页失败: %v", err)
	}
	if res.StatusCode != http.StatusOK {
		t.Fatalf("首页状态码期望 200，实际 %d", res.StatusCode)
	}

	mustHave := []string{
		`<html lang="zh-CN">`,
		`/assets/index-abc123.js`,
		`<title>备课箱商城｜小学科学课件_教学设计_逐字稿_新课标同步备课资源</title>`,
		`专注小学科学同步备课资料`,
		`"@type":"WebSite"`,
		`"@type":"EducationalOrganization"`,
		`/categories/kejiaoban`,
		`/products/jk-3a-01`,
		DefaultOGImageURL,
	}
	for _, needle := range mustHave {
		if !strings.Contains(res.HTML, needle) {
			t.Errorf("首页渲染 HTML 缺少期望片段 %q\n输出:\n%s", needle, res.HTML)
		}
	}
	if strings.Contains(res.HTML, "小初高") {
		t.Errorf("首页不应输出遗留的泛学科描述，实际输出:\n%s", res.HTML)
	}

	// 等待短 TTL 过期后触发 If-None-Match 304 校验
	time.Sleep(20 * time.Millisecond)
	res2, err := svc.Render(context.Background(), "/", "https://shop.beikevip.site")
	if err != nil || res2.StatusCode != http.StatusOK {
		t.Fatalf("二次渲染首页失败: %v", err)
	}
	if atomic.LoadInt32(&hitCount) < 2 {
		t.Fatalf("期望 TTL 过期后发起带 ETag 的条件请求，实际命中 %d 次", hitCount)
	}
	if !strings.Contains(res2.HTML, `/assets/index-abc123.js`) {
		t.Fatalf("304 续期后应保留前端静态脚本引用")
	}
}

func TestHTMLShellServiceProductDetailAnd404Guard(t *testing.T) {
	svc, db := setupHTMLShellTestEnv(t, "")

	cat := models.Category{
		Slug:     "sujiaoban",
		NameJSON: models.JSON{"zh-CN": "苏教版"},
		IsActive: true,
	}
	if err := db.Create(&cat).Error; err != nil {
		t.Fatalf("创建分类失败: %v", err)
	}

	// 1. 有效上架商品（带相对图片路径、短描述需触发四件套结构化兜底、含启用 SKU）
	activeProd := models.Product{
		CategoryID:      cat.ID,
		Slug:            "sj-4a-sound",
		TitleJSON:       models.JSON{"zh-CN": "声音的产生"},
		SeoMetaJSON:     models.JSON{"description": models.JSON{"zh-CN": "声音的产生"}},
		DescriptionJSON: models.JSON{"zh-CN": "四年级上册科学优质公开课全套素材"},
		ContentJSON:     models.JSON{"zh-CN": "<p>本课包含实验演示视频与可编辑希沃/PPT课件。</p>"},
		Images:          models.StringArray{"/uploads/product/2026/10/cover.png"},
		Tags:            models.StringArray{"苏教版", "四年级上册"},
		PriceAmount:     models.NewMoneyFromDecimal(decimal.NewFromFloat(12.8)),
		ManualStockTotal: 99,
		PurchaseType:    constants.ProductPurchaseGuest,
		FulfillmentType: constants.FulfillmentTypeManual,
		IsActive:        true,
	}
	if err := db.Create(&activeProd).Error; err != nil {
		t.Fatalf("创建有效商品失败: %v", err)
	}
	sku := models.ProductSKU{
		ProductID:        activeProd.ID,
		SKUCode:          "SJ-4A-SOUND-FULL",
		PriceAmount:      models.NewMoneyFromDecimal(decimal.NewFromFloat(12.8)),
		ManualStockTotal: 50,
		IsActive:         true,
	}
	if err := db.Create(&sku).Error; err != nil {
		t.Fatalf("创建商品启用 SKU 失败: %v", err)
	}

	// 2. 下架商品 -> 必须 404
	inactiveProd := models.Product{
		CategoryID:      cat.ID,
		Slug:            "offline-item",
		TitleJSON:       models.JSON{"zh-CN": "已下架课件"},
		PriceAmount:     models.NewMoneyFromDecimal(decimal.NewFromInt(10)),
		PurchaseType:    constants.ProductPurchaseGuest,
		FulfillmentType: constants.FulfillmentTypeManual,
		IsActive:        false,
	}
	if err := db.Create(&inactiveProd).Error; err != nil {
		t.Fatalf("创建下架商品失败: %v", err)
	}

	// 3. 商品自身启用但所有 SKU 均已停用（落实 CONTEXT.md / ADR-0001）-> 必须 404
	allSKUDisabledProd := models.Product{
		CategoryID:      cat.ID,
		Slug:            "all-sku-disabled",
		TitleJSON:       models.JSON{"zh-CN": "规格全停用课件"},
		PriceAmount:     models.NewMoneyFromDecimal(decimal.NewFromInt(15)),
		PurchaseType:    constants.ProductPurchaseGuest,
		FulfillmentType: constants.FulfillmentTypeAuto,
		IsActive:        true,
	}
	if err := db.Create(&allSKUDisabledProd).Error; err != nil {
		t.Fatalf("创建全停用 SKU 商品失败: %v", err)
	}
	disabledSKU := models.ProductSKU{
		ProductID:   allSKUDisabledProd.ID,
		SKUCode:     "DISABLED-SKU-01",
		PriceAmount: models.NewMoneyFromDecimal(decimal.NewFromInt(15)),
		IsActive:    true,
	}
	if err := db.Create(&disabledSKU).Error; err != nil {
		t.Fatalf("创建停用 SKU 失败: %v", err)
	}
	if err := db.Model(&models.ProductSKU{}).Where("id = ?", disabledSKU.ID).Update("is_active", false).Error; err != nil {
		t.Fatalf("停用 SKU 失败: %v", err)
	}

	t.Run("在售有效商品返回 200 并注入绝对路径图片、结构化兜底描述与完整 JSON-LD", func(t *testing.T) {
		res, err := svc.Render(context.Background(), "/products/sj-4a-sound", "https://shop.beikevip.site")
		if err != nil {
			t.Fatalf("渲染商品详情失败: %v", err)
		}
		if res.StatusCode != http.StatusOK {
			t.Fatalf("期望 200，实际 %d", res.StatusCode)
		}

		expectedDesc := "苏教版《声音的产生》小学科学同步备课资源包，内含可编辑PPT课件、教学设计、课堂逐字稿与同步练习源文件，对齐2022版科学新课标，支持微信/支付宝免注册扫码购买、秒级自动发货。"
		mustHave := []string{
			`<html lang="zh-CN">`,
			expectedDesc,
			`<meta property="og:image" content="https://shop.beikevip.site/uploads/product/2026/10/cover.png" />`,
			`<meta name="twitter:image" content="https://shop.beikevip.site/uploads/product/2026/10/cover.png" />`,
			`"https://shop.beikevip.site/uploads/product/2026/10/cover.png"`,
			`"@type":"Product"`,
			`"@type":"BreadcrumbList"`,
			`"sku":"SJ-4A-SOUND-FULL"`,
			`"name":"备课箱"`,
			`<h1>声音的产生</h1>`,
			`/categories/sujiaoban`,
			`本课包含实验演示视频与可编辑希沃/PPT课件。`,
		}
		for _, s := range mustHave {
			if !strings.Contains(res.HTML, s) {
				t.Errorf("商品详情页缺少期望内容 %q\n实际 HTML:\n%s", s, res.HTML)
			}
		}
		if strings.Contains(res.HTML, "noindex") {
			t.Errorf("有效商品页不应包含 noindex")
		}
	})

	t.Run("不存在的商品返回真实 HTTP 404 与 noindex", func(t *testing.T) {
		res, err := svc.Render(context.Background(), "/products/non-existent-slug", "https://shop.beikevip.site")
		if err != nil {
			t.Fatalf("渲染失败: %v", err)
		}
		if res.StatusCode != http.StatusNotFound {
			t.Fatalf("不存在商品期望 404，实际 %d", res.StatusCode)
		}
		if !strings.Contains(res.HTML, `content="noindex, nofollow"`) {
			t.Errorf("404 页面应包含 noindex, nofollow")
		}
	})

	t.Run("已下架商品与全停用 SKU 商品均返回真实 HTTP 404", func(t *testing.T) {
		for _, slug := range []string{"offline-item", "all-sku-disabled"} {
			res, err := svc.Render(context.Background(), "/products/"+slug, "https://shop.beikevip.site")
			if err != nil {
				t.Fatalf("渲染 %s 失败: %v", slug, err)
			}
			if res.StatusCode != http.StatusNotFound {
				t.Fatalf("不可见商品 %s 期望 404，实际 %d", slug, res.StatusCode)
			}
		}
	})
}

func TestHTMLShellServiceCategoryAndBlogAndPrivateRoutes(t *testing.T) {
	svc, db := setupHTMLShellTestEnv(t, "")

	activeCat := models.Category{
		Slug:     "yuejiaoban",
		NameJSON: models.JSON{"zh-CN": "粤教版"},
		IsActive: true,
	}
	inactiveCat := models.Category{
		Slug:     "legacy-cat",
		NameJSON: models.JSON{"zh-CN": "旧版停用分类"},
		IsActive: true,
	}
	if err := db.Create(&activeCat).Error; err != nil {
		t.Fatalf("创建启用分类失败: %v", err)
	}
	if err := db.Create(&inactiveCat).Error; err != nil {
		t.Fatalf("创建停用分类失败: %v", err)
	}
	if err := db.Model(&models.Category{}).Where("id = ?", inactiveCat.ID).Update("is_active", false).Error; err != nil {
		t.Fatalf("更新分类停用状态失败: %v", err)
	}

	catProd := models.Product{
		CategoryID:      activeCat.ID,
		Slug:            "yj-5a-light",
		TitleJSON:       models.JSON{"zh-CN": "光的传播路径"},
		PriceAmount:     models.NewMoneyFromDecimal(decimal.NewFromInt(12)),
		PurchaseType:    constants.ProductPurchaseGuest,
		FulfillmentType: constants.FulfillmentTypeAuto,
		IsActive:        true,
	}
	if err := db.Create(&catProd).Error; err != nil {
		t.Fatalf("创建分类下商品失败: %v", err)
	}

	pubTime := time.Date(2026, 9, 1, 10, 0, 0, 0, time.UTC)
	dupSummary := "新课标倡导以探究实践为核心组织小学科学课堂实验活动。"
	publishedBlog := models.Post{
		Slug:        "inquiry-class-tips",
		Type:        constants.PostTypeBlog,
		TitleJSON:   models.JSON{"zh-CN": "如何高效组织40分钟科学探究课"},
		SummaryJSON: models.JSON{"zh-CN": dupSummary},
		ContentJSON: models.JSON{"zh-CN": "<p>" + dupSummary + "</p><p>第二段展开具体的器材分组管理方法。</p>"},
		Thumbnail:   "/uploads/post/2026/09/thumb.jpg",
		IsPublished: true,
		PublishedAt: &pubTime,
	}
	draftBlog := models.Post{
		Slug:        "unpublished-draft",
		Type:        constants.PostTypeBlog,
		TitleJSON:   models.JSON{"zh-CN": "未发布草稿"},
		IsPublished: false,
	}
	if err := db.Create(&publishedBlog).Error; err != nil {
		t.Fatalf("创建已发布博客失败: %v", err)
	}
	if err := db.Create(&draftBlog).Error; err != nil {
		t.Fatalf("创建草稿博客失败: %v", err)
	}

	t.Run("启用分类返回 200 且注入专属标题、面包屑与商品列表内链", func(t *testing.T) {
		res, err := svc.Render(context.Background(), "/categories/yuejiaoban", "https://shop.beikevip.site")
		if err != nil {
			t.Fatalf("渲染分类页失败: %v", err)
		}
		if res.StatusCode != http.StatusOK {
			t.Fatalf("有效分类期望 200，实际 %d", res.StatusCode)
		}
		mustHave := []string{
			`<title>粤教版小学科学课件｜按年级单元选课 - 备课箱</title>`,
			`<h1>粤教版小学科学课件</h1>`,
			`"@type":"BreadcrumbList"`,
			`/products/yj-5a-light`,
		}
		for _, s := range mustHave {
			if !strings.Contains(res.HTML, s) {
				t.Errorf("分类页缺少期望内容 %q\n实际 HTML:\n%s", s, res.HTML)
			}
		}
	})

	t.Run("不存在分类或停用分类返回真实 HTTP 404", func(t *testing.T) {
		for _, p := range []string{"/categories/not-found-cat", "/categories/legacy-cat"} {
			res, err := svc.Render(context.Background(), p, "https://shop.beikevip.site")
			if err != nil {
				t.Fatalf("渲染 %s 失败: %v", p, err)
			}
			if res.StatusCode != http.StatusNotFound {
				t.Fatalf("路径 %s 期望 404，实际 %d", p, res.StatusCode)
			}
		}
	})

	t.Run("已发布博客返回 200、注入教研组 BlogPosting JSON-LD 并对首段重复摘要去重", func(t *testing.T) {
		res, err := svc.Render(context.Background(), "/blog/inquiry-class-tips", "https://shop.beikevip.site")
		if err != nil {
			t.Fatalf("渲染博客详情失败: %v", err)
		}
		if res.StatusCode != http.StatusOK {
			t.Fatalf("已发布博客期望 200，实际 %d", res.StatusCode)
		}
		mustHave := []string{
			`"@type":"BlogPosting"`,
			`"https://shop.beikevip.site/uploads/post/2026/09/thumb.jpg"`,
			`"备课箱小学科学教研组"`,
			`https://beikevip.site/about/#research-team`,
			`第二段展开具体的器材分组管理方法。`,
		}
		for _, s := range mustHave {
			if !strings.Contains(res.HTML, s) {
				t.Errorf("博客详情页缺少期望内容 %q\n实际 HTML:\n%s", s, res.HTML)
			}
		}
		if strings.Contains(res.HTML, `<p class="summary">`) {
			t.Errorf("正文开头已包含摘要时，#app 快照不应重复渲染 <p class=\"summary\">，实际输出:\n%s", res.HTML)
		}
	})

	t.Run("不存在或未发布博客返回真实 HTTP 404", func(t *testing.T) {
		for _, p := range []string{"/blog/missing-post", "/blog/unpublished-draft"} {
			res, err := svc.Render(context.Background(), p, "https://shop.beikevip.site")
			if err != nil {
				t.Fatalf("渲染 %s 失败: %v", p, err)
			}
			if res.StatusCode != http.StatusNotFound {
				t.Fatalf("路径 %s 期望 404，实际 %d", p, res.StatusCode)
			}
		}
	})

	t.Run("游客查单与私密交易鉴权路由返回 200 + noindex", func(t *testing.T) {
		privateRoutes := []string{
			"/cart",
			"/checkout",
			"/pay",
			"/payment",
			"/guest/orders",
			"/guest/orders/DJ202610290001",
			"/orders/DJ202610290002",
			"/recharge-orders/RC202610290001",
			"/me",
			"/me/orders",
			"/me/wallet",
			"/auth/login",
			"/auth/register",
		}
		for _, routePath := range privateRoutes {
			res, err := svc.Render(context.Background(), routePath, "https://shop.beikevip.site")
			if err != nil {
				t.Fatalf("渲染私密路由 %s 失败: %v", routePath, err)
			}
			if res.StatusCode != http.StatusOK {
				t.Fatalf("合法私密路由 %s 期望 200，实际 %d", routePath, res.StatusCode)
			}
			if !strings.Contains(res.HTML, `content="noindex, nofollow"`) {
				t.Errorf("私密路由 %s 必须注入 noindex, nofollow", routePath)
			}
			if strings.Contains(res.HTML, `rel="canonical"`) {
				t.Errorf("私密路由 %s 不应输出 canonical", routePath)
			}
		}
	})

	t.Run("随机未知路径返回真实 HTTP 404 + noindex", func(t *testing.T) {
		unknownRoutes := []string{
			"/totally-unknown-route-xyz",
			"/products/foo/bar",
			"/categories",
			"/random/deep/path",
		}
		for _, routePath := range unknownRoutes {
			res, err := svc.Render(context.Background(), routePath, "https://shop.beikevip.site")
			if err != nil {
				t.Fatalf("渲染未知路径 %s 失败: %v", routePath, err)
			}
			if res.StatusCode != http.StatusNotFound {
				t.Fatalf("未知路径 %s 期望 404，实际 %d", routePath, res.StatusCode)
			}
			if !strings.Contains(res.HTML, `content="noindex, nofollow"`) {
				t.Errorf("未知路径 %s 必须包含 noindex, nofollow", routePath)
			}
		}
	})
}
