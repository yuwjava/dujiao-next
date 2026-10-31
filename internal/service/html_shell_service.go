package service

import (
	"context"
	"encoding/json"
	"html"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/dujiao-next/internal/constants"
	"github.com/dujiao-next/internal/htmltext"
	"github.com/dujiao-next/internal/models"
	"github.com/dujiao-next/internal/repository"
)

const (
	// UserTemplateURLEnvKey 前端入口 HTML 模板地址环境变量名
	UserTemplateURLEnvKey = "DJ_USER_TEMPLATE_URL"
	// DefaultUserTemplateURL 双容器内网默认前端入口模板地址
	DefaultUserTemplateURL = "http://user/index.html"
	// DefaultShopBaseURL 默认商城站点根域名
	DefaultShopBaseURL = "https://shop.beikevip.site"
	// DefaultOGImageURL 默认横版社交分享图（1200x630）
	DefaultOGImageURL = "https://beikevip.site/assets/beikexiang_og_1200x630.png"

	defaultTemplateCacheTTL     = 30 * time.Second
	defaultTemplateErrorBackoff = 5 * time.Second
	defaultTemplateFetchTimeout = 2 * time.Second

	verticalHomeTitle       = "备课箱商城｜小学科学课件_教学设计_逐字稿_新课标同步备课资源"
	verticalHomeDescription = "备课箱商城专注小学科学同步备课资料，覆盖教科版、苏教版、粤教版、人教版、大象版1-6年级可编辑PPT课件、教学设计、课堂逐字稿与同步练习，支持微信支付宝免注册自动发货。"
	verticalHomeKeywords    = "小学科学课件,小学科学教案,科学逐字稿,教科版科学课件,苏教版科学课件,粤教版科学课件,人教版科学课件,大象版科学课件,2022新课标科学,备课箱商城"
)

// fallbackMinimalHTMLShell 当内网前端容器暂不可达（如单测或本地独立启动）时使用的标准 HTML5 最小壳
const fallbackMinimalHTMLShell = `<!doctype html>
<html lang="zh-CN">
  <head>
    <meta charset="UTF-8" />
    <meta name="viewport" content="width=device-width, initial-scale=1.0" />
    <title></title>
  </head>
  <body>
    <div id="app"></div>
  </body>
</html>
`

var (
	htmlOpenTagRegex  = regexp.MustCompile(`(?i)<html\b([^>]*)>`)
	htmlLangAttrRegex = regexp.MustCompile(`(?i)\blang\s*=\s*("[^"]*"|'[^']*'|[^\s>]+)`)
	titleTagRegex     = regexp.MustCompile(`(?is)<title\b[^>]*>.*?</title>`)
	headCloseTagRegex = regexp.MustCompile(`(?i)</head>`)
	appMountDivRegex  = regexp.MustCompile(`(?is)<div\s+id=["']app["']\s*>\s*</div>`)
	whitespaceRegex   = regexp.MustCompile(`\s+`)
)

// HTMLShellOptions 动态 HTML 壳服务构造参数
type HTMLShellOptions struct {
	ProductRepo    repository.ProductRepository
	CategoryRepo   repository.CategoryRepository
	PostRepo       repository.PostRepository
	SettingService *SettingService
	ProductService *ProductService
	TemplateURL    string
	TemplateTTL    time.Duration
	HTTPClient     *http.Client
}

// HTMLPageResult 页面级 HTML 渲染结果
type HTMLPageResult struct {
	StatusCode int
	HTML       string
}

type pageSeoPayload struct {
	StatusCode  int
	Title       string
	Description string
	Keywords    string
	Canonical   string
	OGType      string
	OGImage     string
	NoIndex     bool
	JSONLD      []map[string]interface{}
	Snapshot    string
}

// HTMLShellService 负责拉取/缓存前端入口模板、执行真实 404 守卫并动态注入 SEO 标签与首屏语义快照
type HTMLShellService struct {
	productRepo    repository.ProductRepository
	categoryRepo   repository.CategoryRepository
	postRepo       repository.PostRepository
	settingService *SettingService
	productService *ProductService

	templateURL string
	templateTTL time.Duration
	httpClient  *http.Client

	mu         sync.RWMutex
	cachedHTML string
	cachedETag string
	expiresAt  time.Time
}

// NewHTMLShellService 创建页面级 SEO HTML 壳注入服务
func NewHTMLShellService(opts HTMLShellOptions) *HTMLShellService {
	tplURL := strings.TrimSpace(opts.TemplateURL)
	if tplURL == "" {
		tplURL = strings.TrimSpace(os.Getenv(UserTemplateURLEnvKey))
	}
	if tplURL == "" {
		tplURL = DefaultUserTemplateURL
	}

	ttl := opts.TemplateTTL
	if ttl <= 0 {
		ttl = defaultTemplateCacheTTL
	}

	client := opts.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: defaultTemplateFetchTimeout}
	}

	return &HTMLShellService{
		productRepo:    opts.ProductRepo,
		categoryRepo:   opts.CategoryRepo,
		postRepo:       opts.PostRepo,
		settingService: opts.SettingService,
		productService: opts.ProductService,
		templateURL:    tplURL,
		templateTTL:    ttl,
		httpClient:     client,
	}
}

// Render 针对指定请求路径与站点根域名生成完整 HTML 页面及真实 HTTP 状态码（200 或 404）
func (s *HTMLShellService) Render(ctx context.Context, requestPath string, baseURL string) (*HTMLPageResult, error) {
	normalizedBase := normalizeSiteBaseURL(baseURL)
	payload := s.buildPagePayload(requestPath, normalizedBase)
	shell := s.loadTemplateShell(ctx)
	rendered := injectIntoHTMLShell(shell, payload)
	return &HTMLPageResult{
		StatusCode: payload.StatusCode,
		HTML:       rendered,
	}, nil
}

// loadTemplateShell 按短 TTL 与 ETag 拉取并缓存前端编译好的 index.html，失败时优雅回退内置最小壳
func (s *HTMLShellService) loadTemplateShell(ctx context.Context) string {
	now := time.Now()

	s.mu.RLock()
	if s.cachedHTML != "" && now.Before(s.expiresAt) {
		cached := s.cachedHTML
		s.mu.RUnlock()
		return cached
	}
	prevHTML := s.cachedHTML
	prevETag := s.cachedETag
	s.mu.RUnlock()

	if strings.TrimSpace(s.templateURL) == "" {
		return fallbackMinimalHTMLShell
	}

	fetchCtx, cancel := context.WithTimeout(ctx, defaultTemplateFetchTimeout)
	defer cancel()

	req, err := http.NewRequestWithContext(fetchCtx, http.MethodGet, s.templateURL, nil)
	if err != nil {
		return s.fallbackTemplateOnError(prevHTML)
	}
	if prevETag != "" && prevHTML != "" {
		req.Header.Set("If-None-Match", prevETag)
	}

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return s.fallbackTemplateOnError(prevHTML)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified && prevHTML != "" {
		s.mu.Lock()
		s.expiresAt = time.Now().Add(s.templateTTL)
		s.mu.Unlock()
		return prevHTML
	}

	if resp.StatusCode != http.StatusOK {
		return s.fallbackTemplateOnError(prevHTML)
	}

	bodyBytes, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024))
	if err != nil {
		return s.fallbackTemplateOnError(prevHTML)
	}
	bodyStr := string(bodyBytes)
	if !strings.Contains(strings.ToLower(bodyStr), "<head") || !strings.Contains(bodyStr, "app") {
		return s.fallbackTemplateOnError(prevHTML)
	}

	newETag := strings.TrimSpace(resp.Header.Get("ETag"))
	s.mu.Lock()
	s.cachedHTML = bodyStr
	if newETag != "" {
		s.cachedETag = newETag
	}
	s.expiresAt = time.Now().Add(s.templateTTL)
	s.mu.Unlock()

	return bodyStr
}

func (s *HTMLShellService) fallbackTemplateOnError(prevHTML string) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	if prevHTML != "" {
		s.expiresAt = time.Now().Add(defaultTemplateErrorBackoff)
		return prevHTML
	}
	// 内网不可达时短暂缓存最小壳，避免高并发下每个请求都阻塞等待超时
	s.cachedHTML = fallbackMinimalHTMLShell
	s.expiresAt = time.Now().Add(defaultTemplateErrorBackoff)
	return fallbackMinimalHTMLShell
}

// buildPagePayload 对齐前端合法路由表与商品/分类/文章可见性规则构造页面 SEO 载荷
func (s *HTMLShellService) buildPagePayload(rawPath string, baseURL string) pageSeoPayload {
	cleanPath, validFormat := normalizePageRoutePath(rawPath)
	if !validFormat {
		return s.buildNotFoundPayload(baseURL)
	}

	// 1. 静态公开页面精确匹配
	switch cleanPath {
	case "/":
		return s.buildHomePayload(baseURL)
	case "/products":
		return s.buildProductsPayload(baseURL)
	case "/blog":
		return s.buildPostListPayload(baseURL, constants.PostTypeBlog)
	case "/notice":
		return s.buildPostListPayload(baseURL, constants.PostTypeNotice)
	case "/about":
		return s.buildAboutPayload(baseURL)
	case "/terms":
		return s.buildLegalPayload(baseURL, "terms")
	case "/privacy":
		return s.buildLegalPayload(baseURL, "privacy")
	}

	// 2. 前端合法的私密 / 交易 / 鉴权路由（返回 200 + noindex, nofollow，保障交易与登录流程正常运转）
	if title, matched := matchPrivateSPARoute(cleanPath); matched {
		return pageSeoPayload{
			StatusCode:  http.StatusOK,
			Title:       title + " - 备课箱商城",
			Description: verticalHomeDescription,
			OGType:      "website",
			OGImage:     DefaultOGImageURL,
			NoIndex:     true,
		}
	}

	// 3. 动态实体详情路由匹配
	segments := strings.Split(strings.TrimPrefix(cleanPath, "/"), "/")
	if len(segments) == 2 && segments[1] != "" {
		decodedSlug, err := url.PathUnescape(segments[1])
		if err != nil || strings.TrimSpace(decodedSlug) == "" || strings.Contains(decodedSlug, "/") {
			return s.buildNotFoundPayload(baseURL)
		}
		switch segments[0] {
		case "categories":
			return s.buildCategoryPayload(baseURL, decodedSlug)
		case "products":
			return s.buildProductDetailPayload(baseURL, decodedSlug)
		case "blog":
			return s.buildPostDetailPayload(baseURL, decodedSlug)
		}
	}

	// 4. 其余未匹配路径统一返回真实 HTTP 404 + noindex, nofollow
	return s.buildNotFoundPayload(baseURL)
}

// buildHomePayload 构建商城首页 SEO 载荷与首屏分类/热销商品快照
func (s *HTMLShellService) buildHomePayload(baseURL string) pageSeoPayload {
	title, desc, keywords, ogImg := s.resolveGlobalHomeSEO(baseURL)
	canonical := baseURL + "/"

	categories := s.listActiveCategoriesSafe()
	products := s.listActiveProductsSafe("", nil, 24)

	var snap strings.Builder
	snap.WriteString(`<main class="seo-shell-home">`)
	snap.WriteString(`<h1>`)
	snap.WriteString(html.EscapeString(title))
	snap.WriteString(`</h1>`)
	snap.WriteString(`<p>`)
	snap.WriteString(html.EscapeString(desc))
	snap.WriteString(`</p>`)
	snap.WriteString(renderSiteNavLinksHTML())

	if len(categories) > 0 {
		snap.WriteString(`<section><h2>教材版本与年级分类</h2><ul>`)
		for _, cat := range categories {
			catName := localizedJSONText(cat.NameJSON, cat.Slug)
			catHref := "/categories/" + url.PathEscape(cat.Slug)
			snap.WriteString(`<li><a href="`)
			snap.WriteString(html.EscapeString(catHref))
			snap.WriteString(`">`)
			snap.WriteString(html.EscapeString(catName))
			snap.WriteString(`</a></li>`)
		}
		snap.WriteString(`</ul></section>`)
	}

	if len(products) > 0 {
		snap.WriteString(`<section><h2>精选小学科学同步备课资源</h2><ul>`)
		for _, p := range products {
			pTitle := localizedJSONText(p.TitleJSON, p.Slug)
			pHref := "/products/" + url.PathEscape(p.Slug)
			snap.WriteString(`<li><a href="`)
			snap.WriteString(html.EscapeString(pHref))
			snap.WriteString(`">`)
			snap.WriteString(html.EscapeString(pTitle))
			snap.WriteString(`</a></li>`)
		}
		snap.WriteString(`</ul></section>`)
	}
	snap.WriteString(`</main>`)

	jsonLD := []map[string]interface{}{
		{
			"@context": "https://schema.org",
			"@type":    "WebSite",
			"name":     "备课箱商城",
			"url":      canonical,
			"potentialAction": map[string]interface{}{
				"@type":       "SearchAction",
				"target":      baseURL + "/products?search={search_term_string}",
				"query-input": "required name=search_term_string",
			},
		},
		{
			"@context":    "https://schema.org",
			"@type":       "EducationalOrganization",
			"name":        "备课箱",
			"url":         "https://beikevip.site/",
			"description": desc,
			"sameAs": []string{
				canonical,
				"https://beikevip.site/lab/",
				"https://beikevip.site/about/",
			},
		},
	}

	return pageSeoPayload{
		StatusCode:  http.StatusOK,
		Title:       title,
		Description: desc,
		Keywords:    keywords,
		Canonical:   canonical,
		OGType:      "website",
		OGImage:     ogImg,
		JSONLD:      jsonLD,
		Snapshot:    snap.String(),
	}
}

// buildProductsPayload 构建全部商品中心页面 SEO 载荷
func (s *HTMLShellService) buildProductsPayload(baseURL string) pageSeoPayload {
	title := "小学科学全版本课件资源中心 - 备课箱"
	desc := "浏览备课箱小学科学全版本同步备课资源，涵盖教科版、苏教版、粤教版、人教版、大象版1-6年级可编辑PPT课件、教学设计、课堂逐字稿与同步练习源文件。"
	canonical := baseURL + "/products"

	categories := s.listActiveCategoriesSafe()
	products := s.listActiveProductsSafe("", nil, 50)

	var snap strings.Builder
	snap.WriteString(`<main class="seo-shell-products">`)
	snap.WriteString(`<nav aria-label="面包屑"><a href="/">首页</a> / <span>小学科学全版本课件资源中心</span></nav>`)
	snap.WriteString(`<h1>小学科学全版本课件资源中心</h1>`)
	snap.WriteString(`<p>`)
	snap.WriteString(html.EscapeString(desc))
	snap.WriteString(`</p>`)

	if len(categories) > 0 {
		snap.WriteString(`<section><h2>按教材版本选课</h2><ul>`)
		for _, cat := range categories {
			catName := localizedJSONText(cat.NameJSON, cat.Slug)
			snap.WriteString(`<li><a href="/categories/`)
			snap.WriteString(html.EscapeString(url.PathEscape(cat.Slug)))
			snap.WriteString(`">`)
			snap.WriteString(html.EscapeString(catName + "小学科学课件"))
			snap.WriteString(`</a></li>`)
		}
		snap.WriteString(`</ul></section>`)
	}

	if len(products) > 0 {
		snap.WriteString(`<section><h2>上架课件资源列表</h2><ul>`)
		for _, p := range products {
			pTitle := localizedJSONText(p.TitleJSON, p.Slug)
			snap.WriteString(`<li><a href="/products/`)
			snap.WriteString(html.EscapeString(url.PathEscape(p.Slug)))
			snap.WriteString(`">`)
			snap.WriteString(html.EscapeString(pTitle))
			snap.WriteString(`</a></li>`)
		}
		snap.WriteString(`</ul></section>`)
	}
	snap.WriteString(`</main>`)

	breadcrumb := buildBreadcrumbJSONLD([]breadcrumbItem{
		{Name: "首页", URL: baseURL + "/"},
		{Name: "小学科学全版本课件资源中心", URL: canonical},
	})

	return pageSeoPayload{
		StatusCode:  http.StatusOK,
		Title:       title,
		Description: desc,
		Keywords:    verticalHomeKeywords,
		Canonical:   canonical,
		OGType:      "website",
		OGImage:     DefaultOGImageURL,
		JSONLD:      []map[string]interface{}{breadcrumb},
		Snapshot:    snap.String(),
	}
}

// buildCategoryPayload 构建分类页 SEO 载荷；分类不存在或未启用时返回真实 404
func (s *HTMLShellService) buildCategoryPayload(baseURL string, slug string) pageSeoPayload {
	if s.categoryRepo == nil {
		return s.buildNotFoundPayload(baseURL)
	}
	cat, err := s.categoryRepo.GetBySlug(slug)
	if err != nil || cat == nil || !cat.IsActive {
		return s.buildNotFoundPayload(baseURL)
	}

	// 若该分类为二级分类，额外确认其父分类同样处于启用状态
	if cat.ParentID > 0 {
		parent, pErr := s.categoryRepo.GetByID(strconv.FormatUint(uint64(cat.ParentID), 10))
		if pErr != nil || parent == nil || !parent.IsActive {
			return s.buildNotFoundPayload(baseURL)
		}
	}

	catName := localizedJSONText(cat.NameJSON, cat.Slug)
	h1Text := catName + "小学科学课件"
	title := catName + "小学科学课件｜按年级单元选课 - 备课箱"
	desc := catName + "小学科学同步备课资源专区，提供1-6年级上下册可编辑PPT课件、教学设计、课堂逐字稿与随堂练习源文件，付款秒级自动发货。"
	canonical := baseURL + "/categories/" + url.PathEscape(cat.Slug)
	ogImage := resolveAbsoluteAssetURL(baseURL, cat.Icon)

	catIDStr := strconv.FormatUint(uint64(cat.ID), 10)
	expandedIDs, _ := expandPublicCategoryIDs(s.categoryRepo, catIDStr)
	products := s.listActiveProductsSafe(catIDStr, expandedIDs, 60)

	var snap strings.Builder
	snap.WriteString(`<main class="seo-shell-category">`)
	snap.WriteString(`<nav aria-label="面包屑"><a href="/">首页</a> / <a href="/products">商品中心</a> / <span>`)
	snap.WriteString(html.EscapeString(h1Text))
	snap.WriteString(`</span></nav>`)
	snap.WriteString(`<h1>`)
	snap.WriteString(html.EscapeString(h1Text))
	snap.WriteString(`</h1>`)
	snap.WriteString(`<p>`)
	snap.WriteString(html.EscapeString(desc))
	snap.WriteString(`</p>`)

	if len(products) > 0 {
		snap.WriteString(`<ul>`)
		for _, p := range products {
			pTitle := localizedJSONText(p.TitleJSON, p.Slug)
			snap.WriteString(`<li><a href="/products/`)
			snap.WriteString(html.EscapeString(url.PathEscape(p.Slug)))
			snap.WriteString(`">`)
			snap.WriteString(html.EscapeString(pTitle))
			snap.WriteString(`</a></li>`)
		}
		snap.WriteString(`</ul>`)
	}
	snap.WriteString(`</main>`)

	breadcrumb := buildBreadcrumbJSONLD([]breadcrumbItem{
		{Name: "首页", URL: baseURL + "/"},
		{Name: "商品中心", URL: baseURL + "/products"},
		{Name: h1Text, URL: canonical},
	})

	return pageSeoPayload{
		StatusCode:  http.StatusOK,
		Title:       title,
		Description: desc,
		Keywords:    catName + "," + h1Text + ",小学科学教案,科学逐字稿,备课箱",
		Canonical:   canonical,
		OGType:      "website",
		OGImage:     ogImage,
		JSONLD:      []map[string]interface{}{breadcrumb},
		Snapshot:    snap.String(),
	}
}

// buildProductDetailPayload 构建商品详情页 SEO 载荷；商品不存在或不可见时返回真实 404
func (s *HTMLShellService) buildProductDetailPayload(baseURL string, slug string) pageSeoPayload {
	if s.productRepo == nil {
		return s.buildNotFoundPayload(baseURL)
	}
	// GetBySlug(slug, true) 已严格执行 CONTEXT.md 前台可见性规则：
	// 商品自身启用 + 所属分类启用 + 若有 SKU 则至少存在一个启用 SKU（兼容无 SKU 历史商品）
	product, err := s.productRepo.GetBySlug(slug, true)
	if err != nil || product == nil {
		return s.buildNotFoundPayload(baseURL)
	}

	if s.productService != nil {
		temp := []models.Product{*product}
		if stockErr := s.productService.ApplyAutoStockCounts(temp); stockErr == nil {
			*product = temp[0]
		}
	}

	prodTitle := localizedJSONText(product.TitleJSON, product.Slug)
	catName := localizedJSONText(product.Category.NameJSON, "")
	canonical := baseURL + "/products/" + url.PathEscape(product.Slug)

	// 标题与描述：优先采用后台 seo_meta，否则按商品名解析结果套兜底模板（product_seo_meta.go）
	rawSeoTitle := extractLocalizedFieldFromMap(product.SeoMetaJSON, "title")
	pageTitle := BuildProductPageTitle(rawSeoTitle, prodTitle)

	shortDesc := strings.TrimSpace(localizedJSONText(product.DescriptionJSON, ""))
	rawSeoDesc := extractLocalizedFieldFromMap(product.SeoMetaJSON, "description")
	finalDesc := BuildStructuredProductDescription(rawSeoDesc, prodTitle, shortDesc)

	// 关键词
	rawKeywords := strings.TrimSpace(extractKeywordsFromSeoMeta(product.SeoMetaJSON))
	if rawKeywords == "" && len(product.Tags) > 0 {
		rawKeywords = strings.Join([]string(product.Tags), ",")
	}
	if rawKeywords == "" {
		rawKeywords = verticalHomeKeywords
	}

	// 绝对路径商品图片列表
	absImages := collectProductAbsoluteImages(baseURL, product)
	primaryImage := DefaultOGImageURL
	if len(absImages) > 0 {
		primaryImage = absImages[0]
	} else {
		absImages = []string{DefaultOGImageURL}
	}

	// SKU 编码、展示价格与库存可用性
	skuCode, displayPrice, availability := resolveProductOfferMeta(product, s.productService != nil)

	productSchema := map[string]interface{}{
		"@context":    "https://schema.org",
		"@type":       "Product",
		"name":        prodTitle,
		"description": finalDesc,
		"image":       absImages,
		"sku":         skuCode,
		"brand": map[string]interface{}{
			"@type": "Brand",
			"name":  "备课箱",
		},
		"offers": map[string]interface{}{
			"@type":         "Offer",
			"price":         displayPrice,
			"priceCurrency": "CNY",
			"availability":  availability,
			"url":           canonical,
			"hasMerchantReturnPolicy": map[string]interface{}{
				"@type":                "MerchantReturnPolicy",
				"applicableCountry":    "CN",
				"returnPolicyCategory": "https://schema.org/MerchantReturnNotPermitted",
				"description":          productReturnPolicyText,
			},
		},
	}
	if catName != "" {
		productSchema["category"] = catName
	}

	bcItems := []breadcrumbItem{
		{Name: "首页", URL: baseURL + "/"},
		{Name: "商品中心", URL: baseURL + "/products"},
	}
	if product.Category.Slug != "" && catName != "" {
		bcItems = append(bcItems, breadcrumbItem{
			Name: catName,
			URL:  baseURL + "/categories/" + url.PathEscape(product.Category.Slug),
		})
	}
	bcItems = append(bcItems, breadcrumbItem{Name: prodTitle, URL: canonical})
	breadcrumbSchema := buildBreadcrumbJSONLD(bcItems)

	// 构建 #app 内商品语义快照：正文完整输出为白名单语义 HTML，并附资源包信息与可选规格（product_ssr_content.go）
	contentHTML := sanitizeProductContentHTML(localizedJSONText(product.ContentJSON, ""))
	contentPlain := strings.TrimSpace(htmltext.StripToPlainText(contentHTML))

	var snap strings.Builder
	snap.WriteString(`<main class="seo-shell-product-detail"><article>`)
	snap.WriteString(`<nav aria-label="面包屑"><a href="/">首页</a> / <a href="/products">商品中心</a>`)
	if product.Category.Slug != "" && catName != "" {
		snap.WriteString(` / <a href="/categories/`)
		snap.WriteString(html.EscapeString(url.PathEscape(product.Category.Slug)))
		snap.WriteString(`">`)
		snap.WriteString(html.EscapeString(catName))
		snap.WriteString(`</a>`)
	}
	snap.WriteString(` / <span>`)
	snap.WriteString(html.EscapeString(prodTitle))
	snap.WriteString(`</span></nav>`)

	snap.WriteString(`<h1>`)
	snap.WriteString(html.EscapeString(prodTitle))
	snap.WriteString(`</h1>`)
	snap.WriteString(`<p class="price">价格：¥`)
	snap.WriteString(html.EscapeString(displayPrice))
	snap.WriteString(`</p>`)

	if len(product.Tags) > 0 {
		snap.WriteString(`<ul class="tags">`)
		for _, tag := range product.Tags {
			trimmed := strings.TrimSpace(tag)
			if trimmed == "" {
				continue
			}
			snap.WriteString(`<li>`)
			snap.WriteString(html.EscapeString(trimmed))
			snap.WriteString(`</li>`)
		}
		snap.WriteString(`</ul>`)
	}

	snap.WriteString(`<p class="summary">`)
	if shortDesc != "" {
		snap.WriteString(html.EscapeString(shortDesc))
	} else {
		snap.WriteString(html.EscapeString(finalDesc))
	}
	snap.WriteString(`</p>`)

	snap.WriteString(buildProductResourceInfoHTML(prodTitle, product.FulfillmentType))
	snap.WriteString(buildProductSpecListHTML(product.SKUs))

	if contentHTML != "" && contentPlain != shortDesc {
		snap.WriteString(`<section class="content">`)
		snap.WriteString(contentHTML)
		snap.WriteString(`</section>`)
	}
	snap.WriteString(`</article></main>`)

	return pageSeoPayload{
		StatusCode:  http.StatusOK,
		Title:       pageTitle,
		Description: finalDesc,
		Keywords:    rawKeywords,
		Canonical:   canonical,
		OGType:      "product",
		OGImage:     primaryImage,
		JSONLD:      []map[string]interface{}{productSchema, breadcrumbSchema},
		Snapshot:    snap.String(),
	}
}

// buildPostListPayload 构建 /blog 与 /notice 专属列表页 SEO 载荷
func (s *HTMLShellService) buildPostListPayload(baseURL string, postType string) pageSeoPayload {
	isNotice := postType == constants.PostTypeNotice
	pagePath := "/blog"
	h1Text := "小学科学备课教研专栏"
	title := "小学科学备课教研专栏 - 备课箱"
	desc := "备课箱小学科学教研专栏，分享2022版科学新课标教学设计思路、探究实验课堂组织技巧、公开课磨课指南与同步备课经验。"

	if isNotice {
		pagePath = "/notice"
		h1Text = "商城公告与资源更新通知"
		title = "商城公告与资源更新通知 - 备课箱"
		desc = "备课箱商城最新小学科学课件上架通知、教材版本同步更新动态与平台服务公告。"
	}
	canonical := baseURL + pagePath
	posts := s.listPublishedPostsSafe(postType, 50)

	var snap strings.Builder
	snap.WriteString(`<main class="seo-shell-posts">`)
	snap.WriteString(`<nav aria-label="面包屑"><a href="/">首页</a> / <span>`)
	snap.WriteString(html.EscapeString(h1Text))
	snap.WriteString(`</span></nav>`)
	snap.WriteString(`<h1>`)
	snap.WriteString(html.EscapeString(h1Text))
	snap.WriteString(`</h1>`)
	snap.WriteString(`<p>`)
	snap.WriteString(html.EscapeString(desc))
	snap.WriteString(`</p>`)
	if len(posts) > 0 {
		snap.WriteString(`<ul>`)
		for _, post := range posts {
			pTitle := localizedJSONText(post.TitleJSON, post.Slug)
			snap.WriteString(`<li><a href="/blog/`)
			snap.WriteString(html.EscapeString(url.PathEscape(post.Slug)))
			snap.WriteString(`">`)
			snap.WriteString(html.EscapeString(pTitle))
			snap.WriteString(`</a></li>`)
		}
		snap.WriteString(`</ul>`)
	}
	snap.WriteString(`</main>`)

	breadcrumb := buildBreadcrumbJSONLD([]breadcrumbItem{
		{Name: "首页", URL: baseURL + "/"},
		{Name: h1Text, URL: canonical},
	})

	return pageSeoPayload{
		StatusCode:  http.StatusOK,
		Title:       title,
		Description: desc,
		Keywords:    verticalHomeKeywords,
		Canonical:   canonical,
		OGType:      "website",
		OGImage:     DefaultOGImageURL,
		JSONLD:      []map[string]interface{}{breadcrumb},
		Snapshot:    snap.String(),
	}
}

// buildPostDetailPayload 构建文章/公告详情页 SEO 载荷；不存在或未发布时返回真实 404
func (s *HTMLShellService) buildPostDetailPayload(baseURL string, slug string) pageSeoPayload {
	if s.postRepo == nil {
		return s.buildNotFoundPayload(baseURL)
	}
	post, err := s.postRepo.GetBySlug(slug, true)
	if err != nil || post == nil || !post.IsPublished {
		return s.buildNotFoundPayload(baseURL)
	}

	postTitle := localizedJSONText(post.TitleJSON, post.Slug)
	canonical := baseURL + "/blog/" + url.PathEscape(post.Slug)
	summary := strings.TrimSpace(localizedJSONText(post.SummaryJSON, ""))
	plainContent := strings.TrimSpace(htmltext.StripToPlainText(localizedJSONText(post.ContentJSON, "")))

	desc := summary
	if desc == "" && plainContent != "" {
		desc = truncateRunes(plainContent, 140)
	}
	if desc == "" {
		desc = postTitle + " - 备课箱小学科学教研组分享。"
	}

	absImage := resolveAbsoluteAssetURL(baseURL, post.Thumbnail)
	pubTime := post.CreatedAt
	if post.PublishedAt != nil && !post.PublishedAt.IsZero() {
		pubTime = *post.PublishedAt
	}
	if pubTime.IsZero() {
		pubTime = time.Now().UTC()
	}
	dateISO := pubTime.UTC().Format(time.RFC3339)

	sectionName := "备课专栏"
	sectionPath := "/blog"
	if post.Type == constants.PostTypeNotice {
		sectionName = "商城公告"
		sectionPath = "/notice"
	}

	blogPostingSchema := map[string]interface{}{
		"@context":      "https://schema.org",
		"@type":         "BlogPosting",
		"headline":      postTitle,
		"description":   desc,
		"image":         []string{absImage},
		"datePublished": dateISO,
		"dateModified":  dateISO,
		"mainEntityOfPage": map[string]interface{}{
			"@type": "WebPage",
			"@id":   canonical,
		},
		"author": map[string]interface{}{
			"@type": "Organization",
			"name":  "备课箱小学科学教研组",
			"url":   "https://beikevip.site/about/#research-team",
		},
		"publisher": map[string]interface{}{
			"@type": "Organization",
			"name":  "备课箱",
			"url":   "https://beikevip.site/",
		},
	}

	breadcrumbSchema := buildBreadcrumbJSONLD([]breadcrumbItem{
		{Name: "首页", URL: baseURL + "/"},
		{Name: sectionName, URL: baseURL + sectionPath},
		{Name: postTitle, URL: canonical},
	})

	// 摘要与正文首段去重：若正文纯文本开头已包含摘要，则不重复输出摘要段落
	duplicateSummary := IsSummaryDuplicatedInContent(summary, plainContent)
	excerpt := truncateRunes(plainContent, 2000)

	var snap strings.Builder
	snap.WriteString(`<main class="seo-shell-post-detail"><article>`)
	snap.WriteString(`<nav aria-label="面包屑"><a href="/">首页</a> / <a href="`)
	snap.WriteString(html.EscapeString(sectionPath))
	snap.WriteString(`">`)
	snap.WriteString(html.EscapeString(sectionName))
	snap.WriteString(`</a> / <span>`)
	snap.WriteString(html.EscapeString(postTitle))
	snap.WriteString(`</span></nav>`)
	snap.WriteString(`<h1>`)
	snap.WriteString(html.EscapeString(postTitle))
	snap.WriteString(`</h1>`)
	snap.WriteString(`<p class="byline">作者：<a href="https://beikevip.site/about/#research-team">备课箱小学科学教研组</a> · 发布于 `)
	snap.WriteString(html.EscapeString(pubTime.UTC().Format("2006-01-02")))
	snap.WriteString(`</p>`)

	if summary != "" && !duplicateSummary {
		snap.WriteString(`<p class="summary">`)
		snap.WriteString(html.EscapeString(summary))
		snap.WriteString(`</p>`)
	}
	if excerpt != "" {
		snap.WriteString(`<div class="post-body"><p>`)
		snap.WriteString(html.EscapeString(excerpt))
		snap.WriteString(`</p></div>`)
	}
	snap.WriteString(`</article></main>`)

	return pageSeoPayload{
		StatusCode:  http.StatusOK,
		Title:       postTitle + " - 备课箱",
		Description: desc,
		Keywords:    verticalHomeKeywords,
		Canonical:   canonical,
		OGType:      "article",
		OGImage:     absImage,
		JSONLD:      []map[string]interface{}{blogPostingSchema, breadcrumbSchema},
		Snapshot:    snap.String(),
	}
}

// buildAboutPayload 构建关于我们页面 SEO 载荷与主站互链快照
func (s *HTMLShellService) buildAboutPayload(baseURL string) pageSeoPayload {
	title := "关于备课箱商城 - 备课箱"
	desc := "备课箱专注小学科学新课标同步教学资源研发，提供教科版、苏教版、粤教版、人教版、大象版1-6年级精品课件、教案、逐字稿及免费互动数字实验室。"
	canonical := baseURL + "/about"

	var snap strings.Builder
	snap.WriteString(`<main class="seo-shell-about">`)
	snap.WriteString(`<h1>关于备课箱商城</h1>`)
	snap.WriteString(`<p>`)
	snap.WriteString(html.EscapeString(desc))
	snap.WriteString(`</p>`)
	snap.WriteString(renderSiteNavLinksHTML())
	snap.WriteString(`</main>`)

	breadcrumb := buildBreadcrumbJSONLD([]breadcrumbItem{
		{Name: "首页", URL: baseURL + "/"},
		{Name: "关于备课箱商城", URL: canonical},
	})

	return pageSeoPayload{
		StatusCode:  http.StatusOK,
		Title:       title,
		Description: desc,
		Keywords:    verticalHomeKeywords,
		Canonical:   canonical,
		OGType:      "website",
		OGImage:     DefaultOGImageURL,
		JSONLD:      []map[string]interface{}{breadcrumb},
		Snapshot:    snap.String(),
	}
}

// buildLegalPayload 构建服务条款与隐私政策页面 SEO 载荷
func (s *HTMLShellService) buildLegalPayload(baseURL string, kind string) pageSeoPayload {
	h1Text := "服务条款"
	pagePath := "/terms"
	desc := "备课箱商城数字备课资源购买、自动发货交付及售后服务条款说明。"
	legalField := "terms"

	if kind == "privacy" {
		h1Text = "隐私政策"
		pagePath = "/privacy"
		desc = "备课箱商城用户信息保护、订单数据安全与隐私政策说明。"
		legalField = "privacy"
	}

	canonical := baseURL + pagePath
	legalExcerpt := s.resolveLegalExcerpt(legalField)

	var snap strings.Builder
	snap.WriteString(`<main class="seo-shell-legal">`)
	snap.WriteString(`<h1>`)
	snap.WriteString(html.EscapeString(h1Text))
	snap.WriteString(`</h1>`)
	snap.WriteString(`<p>`)
	if legalExcerpt != "" {
		snap.WriteString(html.EscapeString(legalExcerpt))
	} else {
		snap.WriteString(html.EscapeString(desc))
	}
	snap.WriteString(`</p>`)
	snap.WriteString(`</main>`)

	breadcrumb := buildBreadcrumbJSONLD([]breadcrumbItem{
		{Name: "首页", URL: baseURL + "/"},
		{Name: h1Text, URL: canonical},
	})

	return pageSeoPayload{
		StatusCode:  http.StatusOK,
		Title:       h1Text + " - 备课箱",
		Description: desc,
		Keywords:    verticalHomeKeywords,
		Canonical:   canonical,
		OGType:      "website",
		OGImage:     DefaultOGImageURL,
		JSONLD:      []map[string]interface{}{breadcrumb},
		Snapshot:    snap.String(),
	}
}

// buildNotFoundPayload 构建真实 HTTP 404 响应载荷与品牌化引导快照
func (s *HTMLShellService) buildNotFoundPayload(baseURL string) pageSeoPayload {
	var snap strings.Builder
	snap.WriteString(`<main class="seo-shell-not-found">`)
	snap.WriteString(`<h1>404 页面不存在或资源已下架</h1>`)
	snap.WriteString(`<p>抱歉，您访问的备课资源可能已调整或链接输入有误，请前往资源中心查找最新同步课件。</p>`)
	snap.WriteString(renderSiteNavLinksHTML())
	snap.WriteString(`</main>`)

	return pageSeoPayload{
		StatusCode:  http.StatusNotFound,
		Title:       "页面不存在 - 备课箱商城",
		Description: "您访问的页面不存在或商品已下架，请返回备课箱商城浏览最新小学科学课件资源。",
		OGType:      "website",
		OGImage:     DefaultOGImageURL,
		NoIndex:     true,
		Snapshot:    snap.String(),
	}
}

func renderSiteNavLinksHTML() string {
	return `<nav aria-label="站内与生态导航"><ul>` +
		`<li><a href="/">备课箱商城首页</a></li>` +
		`<li><a href="/products">小学科学全版本课件资源中心</a></li>` +
		`<li><a href="/blog">小学科学备课教研专栏</a></li>` +
		`<li><a href="/notice">商城公告</a></li>` +
		`<li><a href="https://beikevip.site/">备课箱官网</a></li>` +
		`<li><a href="https://beikevip.site/lab/">免费小学科学数字实验室</a></li>` +
		`<li><a href="https://beikevip.site/about/">备课箱教研组简介</a></li>` +
		`</ul></nav>`
}

// IsSummaryDuplicatedInContent 判断文章摘要是否与正文提取纯文本后的开头重复
func IsSummaryDuplicatedInContent(summary string, plainContent string) bool {
	normSummary := normalizeCompactText(summary)
	normContent := normalizeCompactText(plainContent)
	if normSummary == "" || normContent == "" {
		return false
	}
	return strings.HasPrefix(normContent, normSummary)
}

func normalizeCompactText(s string) string {
	collapsed := whitespaceRegex.ReplaceAllString(strings.TrimSpace(s), "")
	return collapsed
}

func (s *HTMLShellService) resolveGlobalHomeSEO(baseURL string) (title, desc, keywords, ogImg string) {
	title = verticalHomeTitle
	desc = verticalHomeDescription
	keywords = verticalHomeKeywords
	ogImg = DefaultOGImageURL

	if s.settingService == nil {
		return
	}
	siteCfg, err := s.settingService.GetByKey(constants.SettingKeySiteConfig)
	if err != nil || siteCfg == nil {
		return
	}
	seoRaw, ok := siteCfg["seo"].(map[string]interface{})
	if !ok || seoRaw == nil {
		return
	}

	cfgTitle := strings.TrimSpace(extractLocalizedFieldFromMap(models.JSON(seoRaw), "title"))
	cfgDesc := strings.TrimSpace(extractLocalizedFieldFromMap(models.JSON(seoRaw), "description"))
	cfgKeywords := strings.TrimSpace(extractLocalizedFieldFromMap(models.JSON(seoRaw), "keywords"))
	if rawOG, ok := seoRaw["og_image"].(string); ok && strings.TrimSpace(rawOG) != "" {
		ogImg = resolveAbsoluteAssetURL(baseURL, rawOG)
	}

	if !isGenericOrLegacyHomeTitle(cfgTitle) && !containsLegacyBroadSubjectCopy(cfgTitle) {
		title = cfgTitle
	}
	if cfgDesc != "" && !containsLegacyBroadSubjectCopy(cfgDesc) {
		desc = cfgDesc
	}
	if cfgKeywords != "" && !containsLegacyBroadSubjectCopy(cfgKeywords) {
		keywords = cfgKeywords
	}
	return
}

func isGenericOrLegacyHomeTitle(title string) bool {
	trimmed := strings.TrimSpace(title)
	if trimmed == "" || trimmed == "备课箱" {
		return true
	}
	return strings.EqualFold(trimmed, "Dujiao-Next")
}

func containsLegacyBroadSubjectCopy(text string) bool {
	return strings.Contains(text, "小初高") || strings.Contains(text, "全学科")
}

func (s *HTMLShellService) resolveLegalExcerpt(field string) string {
	if s.settingService == nil {
		return ""
	}
	siteCfg, err := s.settingService.GetByKey(constants.SettingKeySiteConfig)
	if err != nil || siteCfg == nil {
		return ""
	}
	legalRaw, ok := siteCfg["legal"].(map[string]interface{})
	if !ok || legalRaw == nil {
		return ""
	}
	content := extractLocalizedFieldFromMap(models.JSON(legalRaw), field)
	return truncateRunes(htmltext.StripToPlainText(content), 600)
}

func (s *HTMLShellService) listActiveCategoriesSafe() []models.Category {
	if s.categoryRepo == nil {
		return nil
	}
	list, err := s.categoryRepo.ListActive()
	if err != nil {
		return nil
	}
	return list
}

func (s *HTMLShellService) listActiveProductsSafe(categoryID string, categoryIDs []uint, limit int) []models.Product {
	if s.productRepo == nil {
		return nil
	}
	products, _, err := s.productRepo.List(repository.ProductListFilter{
		Page:         1,
		PageSize:     limit,
		CategoryID:   categoryID,
		CategoryIDs:  categoryIDs,
		OnlyActive:   true,
		WithCategory: true,
	})
	if err != nil {
		return nil
	}
	return products
}

func (s *HTMLShellService) listPublishedPostsSafe(postType string, limit int) []models.Post {
	if s.postRepo == nil {
		return nil
	}
	posts, _, err := s.postRepo.List(repository.PostListFilter{
		Page:          1,
		PageSize:      limit,
		Type:          postType,
		OnlyPublished: true,
		OrderBy:       "published_at DESC, created_at DESC",
	})
	if err != nil {
		return nil
	}
	return posts
}

// matchPrivateSPARoute 匹配前端合法的私密、交易、个人中心与鉴权路由
func matchPrivateSPARoute(cleanPath string) (string, bool) {
	switch cleanPath {
	case "/cart":
		return "购物车", true
	case "/checkout":
		return "确认订单", true
	case "/pay", "/payment":
		return "订单支付", true
	case "/guest/orders":
		return "游客订单查询", true
	case "/me":
		return "个人中心", true
	case "/auth/login":
		return "登录", true
	case "/auth/register":
		return "注册", true
	case "/auth/forgot":
		return "找回密码", true
	case "/auth/telegram/callback":
		return "登录回调", true
	}

	if strings.HasPrefix(cleanPath, "/me/") && len(cleanPath) > len("/me/") {
		return "个人中心", true
	}
	if strings.HasPrefix(cleanPath, "/auth/") && len(cleanPath) > len("/auth/") {
		return "账号认证", true
	}

	segments := strings.Split(strings.TrimPrefix(cleanPath, "/"), "/")
	if len(segments) == 2 && segments[1] != "" {
		switch segments[0] {
		case "orders":
			return "订单详情", true
		case "recharge-orders":
			return "充值订单详情", true
		}
	}
	if len(segments) == 3 && segments[0] == "guest" && segments[1] == "orders" && segments[2] != "" {
		return "游客订单详情", true
	}
	return "", false
}

// normalizePageRoutePath 归一化并校验请求路径结构（剔除尾斜杠，拒绝畸形双斜杠）
func normalizePageRoutePath(rawPath string) (string, bool) {
	trimmed := strings.TrimSpace(rawPath)
	if trimmed == "" || trimmed == "/" || trimmed == "/index.html" {
		return "/", true
	}
	if !strings.HasPrefix(trimmed, "/") {
		trimmed = "/" + trimmed
	}
	if strings.Contains(trimmed, "//") {
		return "", false
	}
	cleaned := strings.TrimRight(trimmed, "/")
	if cleaned == "" {
		return "/", true
	}
	return cleaned, true
}

func normalizeSiteBaseURL(baseURL string) string {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if trimmed == "" || trimmed == "http://" || trimmed == "https://" {
		return DefaultShopBaseURL
	}
	return trimmed
}

func resolveAbsoluteAssetURL(baseURL string, rawPath string) string {
	trimmed := strings.TrimSpace(rawPath)
	if trimmed == "" {
		return DefaultOGImageURL
	}
	if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") {
		return trimmed
	}
	normalizedBase := normalizeSiteBaseURL(baseURL)
	if strings.HasPrefix(trimmed, "/") {
		return normalizedBase + trimmed
	}
	return normalizedBase + "/" + trimmed
}

func collectProductAbsoluteImages(baseURL string, product *models.Product) []string {
	if product == nil {
		return nil
	}
	seen := make(map[string]struct{})
	result := make([]string, 0, len(product.Images)+len(product.SKUs))

	addImg := func(raw string) {
		trimmed := strings.TrimSpace(raw)
		if trimmed == "" {
			return
		}
		abs := resolveAbsoluteAssetURL(baseURL, trimmed)
		if _, exists := seen[abs]; exists {
			return
		}
		seen[abs] = struct{}{}
		result = append(result, abs)
	}

	for _, img := range product.Images {
		addImg(img)
	}
	for _, sku := range product.SKUs {
		if sku.IsActive {
			addImg(sku.ImageURL)
		}
	}
	return result
}

func resolveProductOfferMeta(product *models.Product, autoStockLoaded bool) (skuCode string, displayPrice string, availability string) {
	skuCode = product.Slug
	priceDec := product.PriceAmount.Decimal
	hasActiveSKU := false
	inStock := false

	for _, sku := range product.SKUs {
		if !sku.IsActive {
			continue
		}
		if !hasActiveSKU {
			hasActiveSKU = true
			if code := strings.TrimSpace(sku.SKUCode); code != "" {
				skuCode = code
			}
			priceDec = sku.PriceAmount.Decimal
		} else if sku.PriceAmount.Decimal.LessThan(priceDec) {
			priceDec = sku.PriceAmount.Decimal
		}

		if isSKUAvailable(product.FulfillmentType, product.IsMapped, &sku, autoStockLoaded) {
			inStock = true
		}
	}

	if !hasActiveSKU {
		inStock = isProductWithoutSKUAvailable(product, autoStockLoaded)
	}

	availabilityURL := "https://schema.org/InStock"
	if !inStock {
		availabilityURL = "https://schema.org/OutOfStock"
	}
	return skuCode, priceDec.StringFixed(2), availabilityURL
}

func isSKUAvailable(fulfillmentType string, isMapped bool, sku *models.ProductSKU, autoStockLoaded bool) bool {
	if isMapped {
		return sku.UpstreamStock != 0
	}
	if fulfillmentType == constants.FulfillmentTypeAuto {
		if !autoStockLoaded {
			return true
		}
		return sku.AutoStockAvailable > 0
	}
	if sku.ManualStockTotal == constants.ManualStockUnlimited {
		return true
	}
	return sku.ManualStockTotal > 0
}

func isProductWithoutSKUAvailable(product *models.Product, autoStockLoaded bool) bool {
	if product.IsMapped {
		return true
	}
	if product.FulfillmentType == constants.FulfillmentTypeAuto {
		if !autoStockLoaded {
			return true
		}
		return product.AutoStockAvailable > 0
	}
	if product.ManualStockTotal == constants.ManualStockUnlimited {
		return true
	}
	return product.ManualStockTotal > 0
}

type breadcrumbItem struct {
	Name string
	URL  string
}

func buildBreadcrumbJSONLD(items []breadcrumbItem) map[string]interface{} {
	elements := make([]map[string]interface{}, 0, len(items))
	for idx, item := range items {
		elements = append(elements, map[string]interface{}{
			"@type":    "ListItem",
			"position": idx + 1,
			"name":     item.Name,
			"item":     item.URL,
		})
	}
	return map[string]interface{}{
		"@context":        "https://schema.org",
		"@type":           "BreadcrumbList",
		"itemListElement": elements,
	}
}

func localizedJSONText(data models.JSON, fallback string) string {
	if data == nil {
		return fallback
	}
	for _, lang := range []string{"zh-CN", "zh-TW", "en-US"} {
		if val, ok := data[lang]; ok {
			if str, ok := val.(string); ok && strings.TrimSpace(str) != "" {
				return strings.TrimSpace(str)
			}
		}
	}
	for _, val := range data {
		if str, ok := val.(string); ok && strings.TrimSpace(str) != "" {
			return strings.TrimSpace(str)
		}
	}
	return fallback
}

func extractLocalizedFieldFromMap(data models.JSON, field string) string {
	if data == nil {
		return ""
	}
	raw, ok := data[field]
	if !ok || raw == nil {
		return ""
	}
	switch v := raw.(type) {
	case string:
		return strings.TrimSpace(v)
	case models.JSON:
		return localizedJSONText(v, "")
	case map[string]interface{}:
		return localizedJSONText(models.JSON(v), "")
	default:
		return ""
	}
}

func extractKeywordsFromSeoMeta(data models.JSON) string {
	if data == nil {
		return ""
	}
	if str := extractLocalizedFieldFromMap(data, "keywords"); str != "" {
		return str
	}
	if arr, ok := data["keywords"].([]interface{}); ok {
		parts := make([]string, 0, len(arr))
		for _, item := range arr {
			if s, ok := item.(string); ok && strings.TrimSpace(s) != "" {
				parts = append(parts, strings.TrimSpace(s))
			}
		}
		return strings.Join(parts, ",")
	}
	return ""
}

func truncateRunes(s string, maxRunes int) string {
	trimmed := strings.TrimSpace(s)
	if maxRunes <= 0 || utf8.RuneCountInString(trimmed) <= maxRunes {
		return trimmed
	}
	runes := []rune(trimmed)
	return strings.TrimSpace(string(runes[:maxRunes])) + "…"
}

// injectIntoHTMLShell 将语言属性、SEO meta、JSON-LD 与 #app 快照注入前端 HTML 模板壳
func injectIntoHTMLShell(shell string, payload pageSeoPayload) string {
	output := ensureHTMLLangZhCN(shell)
	output = titleTagRegex.ReplaceAllString(output, "")

	headInjection := renderHeadTags(payload)
	if loc := headCloseTagRegex.FindStringIndex(output); loc != nil {
		output = output[:loc[0]] + headInjection + output[loc[0]:]
	} else {
		output = headInjection + output
	}

	if payload.Snapshot != "" {
		if loc := appMountDivRegex.FindStringIndex(output); loc != nil {
			replacement := `<div id="app">` + payload.Snapshot + `</div>`
			output = output[:loc[0]] + replacement + output[loc[1]:]
		}
	}

	return output
}

func ensureHTMLLangZhCN(rawHTML string) string {
	loc := htmlOpenTagRegex.FindStringSubmatchIndex(rawHTML)
	if loc == nil {
		return rawHTML
	}
	attrs := rawHTML[loc[2]:loc[3]]
	var newAttrs string
	if htmlLangAttrRegex.MatchString(attrs) {
		newAttrs = htmlLangAttrRegex.ReplaceAllString(attrs, `lang="zh-CN"`)
	} else {
		newAttrs = ` lang="zh-CN"` + attrs
	}
	return rawHTML[:loc[0]] + "<html" + newAttrs + ">" + rawHTML[loc[1]:]
}

func renderHeadTags(p pageSeoPayload) string {
	var b strings.Builder
	b.WriteString("\n    <title>")
	b.WriteString(html.EscapeString(p.Title))
	b.WriteString("</title>\n")

	if p.NoIndex {
		b.WriteString(`    <meta name="robots" content="noindex, nofollow" />` + "\n")
	}
	if p.Description != "" {
		b.WriteString(`    <meta name="description" content="`)
		b.WriteString(html.EscapeString(p.Description))
		b.WriteString(`" />` + "\n")
	}
	if p.Keywords != "" && !p.NoIndex {
		b.WriteString(`    <meta name="keywords" content="`)
		b.WriteString(html.EscapeString(p.Keywords))
		b.WriteString(`" />` + "\n")
	}
	if p.Canonical != "" && !p.NoIndex {
		b.WriteString(`    <link rel="canonical" href="`)
		b.WriteString(html.EscapeString(p.Canonical))
		b.WriteString(`" />` + "\n")
	}

	ogType := p.OGType
	if ogType == "" {
		ogType = "website"
	}
	ogImg := p.OGImage
	if ogImg == "" {
		ogImg = DefaultOGImageURL
	}

	b.WriteString(`    <meta property="og:site_name" content="备课箱商城" />` + "\n")
	b.WriteString(`    <meta property="og:type" content="`)
	b.WriteString(html.EscapeString(ogType))
	b.WriteString(`" />` + "\n")
	b.WriteString(`    <meta property="og:title" content="`)
	b.WriteString(html.EscapeString(p.Title))
	b.WriteString(`" />` + "\n")
	if p.Description != "" {
		b.WriteString(`    <meta property="og:description" content="`)
		b.WriteString(html.EscapeString(p.Description))
		b.WriteString(`" />` + "\n")
	}
	if p.Canonical != "" && !p.NoIndex {
		b.WriteString(`    <meta property="og:url" content="`)
		b.WriteString(html.EscapeString(p.Canonical))
		b.WriteString(`" />` + "\n")
	}
	b.WriteString(`    <meta property="og:image" content="`)
	b.WriteString(html.EscapeString(ogImg))
	b.WriteString(`" />` + "\n")

	b.WriteString(`    <meta name="twitter:card" content="summary_large_image" />` + "\n")
	b.WriteString(`    <meta name="twitter:title" content="`)
	b.WriteString(html.EscapeString(p.Title))
	b.WriteString(`" />` + "\n")
	if p.Description != "" {
		b.WriteString(`    <meta name="twitter:description" content="`)
		b.WriteString(html.EscapeString(p.Description))
		b.WriteString(`" />` + "\n")
	}
	b.WriteString(`    <meta name="twitter:image" content="`)
	b.WriteString(html.EscapeString(ogImg))
	b.WriteString(`" />` + "\n")

	for _, ld := range p.JSONLD {
		if ld == nil {
			continue
		}
		rawJSON, err := json.Marshal(ld)
		if err != nil {
			continue
		}
		b.WriteString(`    <script type="application/ld+json">`)
		b.Write(rawJSON)
		b.WriteString(`</script>` + "\n")
	}

	return b.String()
}
