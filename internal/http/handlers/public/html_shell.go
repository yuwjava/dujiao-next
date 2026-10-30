package public

import (
	"net/http"

	"github.com/dujiao-next/internal/logger"
	"github.com/dujiao-next/internal/service"

	"github.com/gin-gonic/gin"
)

// ServeHTMLPage 处理非 API 的前台页面 GET/HEAD 请求，动态注入 SEO HTML 壳并执行真实 404 守卫
func (h *Handler) ServeHTMLPage(c *gin.Context) {
	var shellSvc *service.HTMLShellService
	if h != nil && h.Container != nil && h.HTMLShellService != nil {
		shellSvc = h.HTMLShellService
	} else {
		shellSvc = service.NewHTMLShellService(service.HTMLShellOptions{})
	}

	baseURL := service.DefaultShopBaseURL
	if h != nil {
		baseURL = h.resolveSitemapBaseURL(c)
	}

	res, err := shellSvc.Render(c.Request.Context(), c.Request.URL.Path, baseURL)
	if err != nil {
		logger.Errorw("html_shell_render_failed", "path", c.Request.URL.Path, "error", err)
		c.Data(http.StatusOK, "text/html; charset=utf-8", []byte("<!doctype html><html lang=\"zh-CN\"><head><meta charset=\"UTF-8\" /><title>备课箱商城</title></head><body><div id=\"app\"></div></body></html>"))
		return
	}

	c.Header("Cache-Control", "no-cache")
	if c.Request.Method == http.MethodHead {
		c.Header("Content-Type", "text/html; charset=utf-8")
		c.Status(res.StatusCode)
		return
	}
	c.Data(res.StatusCode, "text/html; charset=utf-8", []byte(res.HTML))
}
