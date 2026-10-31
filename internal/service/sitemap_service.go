package service

import (
	"context"
	"encoding/xml"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/dujiao-next/internal/cache"
	"github.com/dujiao-next/internal/repository"
)

// SitemapService 生成 sitemap.xml / robots.txt 内容
type SitemapService struct {
	productRepo  repository.ProductRepository
	categoryRepo repository.CategoryRepository
	postRepo     repository.PostRepository
}

// NewSitemapService 创建 sitemap 服务
func NewSitemapService(
	productRepo repository.ProductRepository,
	categoryRepo repository.CategoryRepository,
	postRepo repository.PostRepository,
) *SitemapService {
	return &SitemapService{
		productRepo:  productRepo,
		categoryRepo: categoryRepo,
		postRepo:     postRepo,
	}
}

const (
	sitemapCacheTTL    = 5 * time.Minute
	sitemapCachePrefix = "sitemap:xml:"
	sitemapMaxFetch    = 50000 // 单次拉取上限，避免极端数据量打爆内存
)

// Generate 生成 sitemap.xml 内容；baseURL 必须是不带尾斜杠的站点根（如 https://example.com）
func (s *SitemapService) Generate(ctx context.Context, baseURL string) (string, error) {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if baseURL == "" {
		return "", fmt.Errorf("sitemap: empty base url")
	}

	cacheKey := sitemapCachePrefix + baseURL
	if cached, err := cache.GetString(ctx, cacheKey); err == nil && cached != "" {
		return cached, nil
	}

	entries, err := s.collectURLs(baseURL)
	if err != nil {
		return "", err
	}

	xmlStr, err := renderSitemapXML(entries)
	if err != nil {
		return "", err
	}

	_ = cache.SetString(ctx, cacheKey, xmlStr, sitemapCacheTTL)
	return xmlStr, nil
}

// GenerateRobots 生成 robots.txt 内容
func (s *SitemapService) GenerateRobots(baseURL string) string {
	baseURL = strings.TrimRight(strings.TrimSpace(baseURL), "/")
	var b strings.Builder
	b.WriteString("User-agent: *\n")
	b.WriteString("Disallow: /api/\n")
	b.WriteString("Disallow: /admin/\n")
	b.WriteString("Disallow: /me/\n")
	b.WriteString("Disallow: /cart\n")
	b.WriteString("Disallow: /checkout\n")
	b.WriteString("Disallow: /pay\n")
	b.WriteString("Disallow: /orders/\n")
	b.WriteString("Disallow: /recharge-orders/\n")
	b.WriteString("Disallow: /guest/\n")
	b.WriteString("Disallow: /auth/\n")
	if baseURL != "" {
		b.WriteString("\n")
		b.WriteString("Sitemap: ")
		b.WriteString(baseURL)
		b.WriteString("/sitemap.xml\n")
	}
	return b.String()
}

// urlEntry sitemap.xml 中的单条 URL
type urlEntry struct {
	XMLName    xml.Name `xml:"url"`
	Loc        string   `xml:"loc"`
	LastMod    string   `xml:"lastmod,omitempty"`
	ChangeFreq string   `xml:"changefreq,omitempty"`
	Priority   string   `xml:"priority,omitempty"`
}

type urlSet struct {
	XMLName xml.Name   `xml:"urlset"`
	Xmlns   string     `xml:"xmlns,attr"`
	URLs    []urlEntry `xml:"url"`
}

func (s *SitemapService) collectURLs(baseURL string) ([]urlEntry, error) {
	// 先拉取启用的分类、上架商品与已发布文章，用以计算静态页面的真实最新更新日期
	categories, err := s.categoryRepo.ListActive()
	if err != nil {
		return nil, fmt.Errorf("sitemap: list categories: %w", err)
	}

	products, _, err := s.productRepo.List(repository.ProductListFilter{
		Page:       1,
		PageSize:   sitemapMaxFetch,
		OnlyActive: true,
	})
	if err != nil {
		return nil, fmt.Errorf("sitemap: list products: %w", err)
	}

	posts, _, err := s.postRepo.List(repository.PostListFilter{
		Page:          1,
		PageSize:      sitemapMaxFetch,
		OnlyPublished: true,
		OrderBy:       "published_at DESC, created_at DESC",
	})
	if err != nil {
		return nil, fmt.Errorf("sitemap: list posts: %w", err)
	}

	var latestContentTime time.Time
	for _, p := range products {
		if !p.UpdatedAt.IsZero() && p.UpdatedAt.After(latestContentTime) {
			latestContentTime = p.UpdatedAt
		}
	}
	for _, post := range posts {
		postMod := post.CreatedAt
		if post.PublishedAt != nil && !post.PublishedAt.IsZero() {
			postMod = *post.PublishedAt
		}
		if !postMod.IsZero() && postMod.After(latestContentTime) {
			latestContentTime = postMod
		}
	}

	staticLastMod := ""
	if !latestContentTime.IsZero() {
		staticLastMod = latestContentTime.UTC().Format("2006-01-02")
	}

	entries := make([]urlEntry, 0, 16+len(categories)+len(products)+len(posts))

	// 1. 静态页面（LastMod 取最新上架商品/已发布文章的真实更新日期；若库为空则省略）
	staticPages := []struct {
		Path       string
		ChangeFreq string
		Priority   string
	}{
		{"/", "daily", "1.0"},
		{"/products", "daily", "0.9"},
		{"/blog", "weekly", "0.6"},
		{"/notice", "weekly", "0.5"},
		{"/about", "monthly", "0.3"},
		{"/terms", "yearly", "0.2"},
		{"/privacy", "yearly", "0.2"},
	}
	for _, p := range staticPages {
		entries = append(entries, urlEntry{
			Loc:        baseURL + p.Path,
			LastMod:    staticLastMod,
			ChangeFreq: p.ChangeFreq,
			Priority:   p.Priority,
		})
	}

	// 2. 启用且有可收录商品的分类（空分类与分类页一样输出 noindex，不进 sitemap）
	catsWithProducts := categoryIDsWithIndexableProducts(categories, products)
	for _, cat := range categories {
		if !catsWithProducts[cat.ID] {
			continue
		}
		entries = append(entries, urlEntry{
			Loc:        baseURL + "/categories/" + url.PathEscape(cat.Slug),
			LastMod:    cat.CreatedAt.UTC().Format("2006-01-02"),
			ChangeFreq: "weekly",
			Priority:   "0.7",
		})
	}

	// 3. 上架的商品（OnlyActive 已含分类启用过滤；后台标记 noindex 的商品不进 sitemap）
	for i := range products {
		p := &products[i]
		if productSEONoIndex(p) {
			continue
		}
		entries = append(entries, urlEntry{
			Loc:        baseURL + "/products/" + url.PathEscape(p.Slug),
			LastMod:    p.UpdatedAt.UTC().Format("2006-01-02"),
			ChangeFreq: "daily",
			Priority:   "0.8",
		})
	}

	// 4. 已发布的博客 / 公告
	for _, post := range posts {
		lastmod := post.CreatedAt
		if post.PublishedAt != nil && !post.PublishedAt.IsZero() {
			lastmod = *post.PublishedAt
		}
		// blog 与 notice 共用 /blog/:slug 详情页（user 前台 Notice.vue 跳转到 /blog/{slug}）
		entries = append(entries, urlEntry{
			Loc:        baseURL + "/blog/" + url.PathEscape(post.Slug),
			LastMod:    lastmod.UTC().Format("2006-01-02"),
			ChangeFreq: "monthly",
			Priority:   "0.5",
		})
	}

	return entries, nil
}

func renderSitemapXML(entries []urlEntry) (string, error) {
	set := urlSet{
		Xmlns: "http://www.sitemaps.org/schemas/sitemap/0.9",
		URLs:  entries,
	}
	body, err := xml.MarshalIndent(set, "", "  ")
	if err != nil {
		return "", err
	}
	return xml.Header + string(body) + "\n", nil
}
