package service

import (
	"regexp"
	"strings"
)

// 百度统计站点 ID 是 32 位小写十六进制；只接受这个格式，配置里写错或混进别的字符时不注入，
// 避免把任意字符串拼进页面脚本。
var baiduTongjiSiteIDRegex = regexp.MustCompile(`^[0-9a-f]{32}$`)

// normalizeBaiduTongjiSiteID 返回可用的站点 ID；不合法时返回空串（即不启用统计）
func normalizeBaiduTongjiSiteID(raw string) string {
	id := strings.ToLower(strings.TrimSpace(raw))
	if !baiduTongjiSiteIDRegex.MatchString(id) {
		return ""
	}
	return id
}

// baiduTongjiSnippet 百度统计官方安装代码。首屏 PV 由 hm.js 加载时自动上报，
// 前台路由切换时再用 _trackPageview 补报，所以这里不需要额外 push。
func baiduTongjiSnippet(siteID string) string {
	return `<script>var _hmt=_hmt||[];(function(){var hm=document.createElement("script");hm.src="https://hm.baidu.com/hm.js?` +
		siteID +
		`";var s=document.getElementsByTagName("script")[0];s.parentNode.insertBefore(hm,s);})();</script>`
}

// injectBaiduTongji 把统计代码放进 </head> 之前；站点 ID 为空时原样返回
func injectBaiduTongji(html string, siteID string) string {
	if siteID == "" {
		return html
	}
	snippet := baiduTongjiSnippet(siteID)
	if loc := headCloseTagRegex.FindStringIndex(html); loc != nil {
		return html[:loc[0]] + snippet + html[loc[0]:]
	}
	return snippet + html
}
