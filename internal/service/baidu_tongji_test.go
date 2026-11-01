package service

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

const testTongjiID = "0123456789abcdef0123456789abcdef"

func TestNormalizeBaiduTongjiSiteID(t *testing.T) {
	cases := map[string]string{
		testTongjiID:                          testTongjiID,
		"  0123456789ABCDEF0123456789ABCDEF ": testTongjiID,
		"":                                    "",
		"0123456789abcdef":                    "",
		`0123456789abcdef0123456789abcde"`:    "",
		"0123456789abcdef0123456789abcdef0":   "",
	}
	for in, want := range cases {
		if got := normalizeBaiduTongjiSiteID(in); got != want {
			t.Errorf("normalizeBaiduTongjiSiteID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHTMLShellServiceInjectsBaiduTongjiOnlyWhenConfigured(t *testing.T) {
	mockServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><html><head><title></title></head><body><div id="app"></div></body></html>`))
	}))
	defer mockServer.Close()

	svc, _ := setupHTMLShellTestEnv(t, mockServer.URL+"/index.html")

	// 没配置：页面里不能出现统计代码
	res, err := svc.Render(context.Background(), "/", "https://shop.example.com")
	if err != nil {
		t.Fatalf("渲染失败: %v", err)
	}
	if strings.Contains(res.HTML, "hm.baidu.com") {
		t.Fatalf("未配置站点 ID 时不应注入百度统计")
	}

	// 配置了：首页与 404 页都只注入一次，且在 </head> 之前
	svc.baiduTongjiID = normalizeBaiduTongjiSiteID(testTongjiID)
	for _, path := range []string{"/", "/products", "/no-such-page"} {
		res, err := svc.Render(context.Background(), path, "https://shop.example.com")
		if err != nil {
			t.Fatalf("渲染 %s 失败: %v", path, err)
		}
		if n := strings.Count(res.HTML, "hm.baidu.com/hm.js?"+testTongjiID); n != 1 {
			t.Fatalf("%s 应恰好注入 1 次百度统计，实际 %d 次", path, n)
		}
		if strings.Index(res.HTML, "hm.baidu.com") > strings.Index(strings.ToLower(res.HTML), "</head>") {
			t.Fatalf("%s 的百度统计应在 </head> 之前", path)
		}
	}
}
