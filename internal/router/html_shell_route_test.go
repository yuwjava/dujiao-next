package router

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	publichandlers "github.com/dujiao-next/internal/http/handlers/public"
	"github.com/dujiao-next/internal/provider"
	"github.com/dujiao-next/internal/service"

	"github.com/gin-gonic/gin"
)

func TestPageNoRouteHandlerDistinguishesAPIAndPages(t *testing.T) {
	gin.SetMode(gin.TestMode)

	container := &provider.Container{
		HTMLShellService: service.NewHTMLShellService(service.HTMLShellOptions{
			TemplateURL: "http://127.0.0.1:1/index.html",
		}),
	}
	pubHandler := publichandlers.New(container)

	r := gin.New()
	r.Use(RequestIDMiddleware())
	r.NoRoute(buildPageNoRouteHandler("/admin", pubHandler, nil))

	t.Run("未知 API 路由返回 JSON 404 而非 HTML", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/api/v1/unknown-endpoint", nil)
		r.ServeHTTP(w, req)

		if w.Code != http.StatusNotFound {
			t.Fatalf("未知 API 期望 404，实际 %d", w.Code)
		}
		if !strings.Contains(w.Header().Get("Content-Type"), "application/json") {
			t.Fatalf("未知 API 应返回 JSON Content-Type，实际 %s", w.Header().Get("Content-Type"))
		}
		var body map[string]interface{}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatalf("解析未知 API JSON 失败: %v", err)
		}
	})

	t.Run("合法公开静态路由返回 200 HTML 壳", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "https://shop.beikevip.site/about", nil)
		r.ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Fatalf("/about 期望 200，实际 %d", w.Code)
		}
		if !strings.Contains(w.Body.String(), `<html lang="zh-CN">`) {
			t.Fatalf("期望返回注入后的中文 HTML 壳，实际:\n%s", w.Body.String())
		}
	})

	t.Run("随机未知页面路由返回真实 404 HTML 壳", func(t *testing.T) {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "https://shop.beikevip.site/totally-unknown-route-xyz", nil)
		r.ServeHTTP(w, req)

		if w.Code != http.StatusNotFound {
			t.Fatalf("未知页面期望 404，实际 %d", w.Code)
		}
		if !strings.Contains(w.Body.String(), `noindex, nofollow`) {
			t.Fatalf("未知页面 HTML 应包含 noindex, nofollow")
		}
	})

	t.Run("HEAD 请求返回一致状态码且无多余报错", func(t *testing.T) {
		wOk := httptest.NewRecorder()
		reqOk := httptest.NewRequest(http.MethodHead, "https://shop.beikevip.site/guest/orders", nil)
		r.ServeHTTP(wOk, reqOk)
		if wOk.Code != http.StatusOK {
			t.Fatalf("HEAD /guest/orders 期望 200，实际 %d", wOk.Code)
		}

		w404 := httptest.NewRecorder()
		req404 := httptest.NewRequest(http.MethodHead, "https://shop.beikevip.site/unknown-page", nil)
		r.ServeHTTP(w404, req404)
		if w404.Code != http.StatusNotFound {
			t.Fatalf("HEAD /unknown-page 期望 404，实际 %d", w404.Code)
		}
	})
}
