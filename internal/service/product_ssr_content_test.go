package service

import (
	"strings"
	"testing"

	"github.com/dujiao-next/internal/constants"
	"github.com/dujiao-next/internal/models"
)

func TestSanitizeProductContentHTML(t *testing.T) {
	cases := []struct {
		name string
		raw  string
		want string
	}{
		{"空内容", "   ", ""},
		{"保留语义标签", "<h3>商品交易说明</h3><ol><li><p><strong>资料内容</strong><br>以word文档提供</p></li></ol>", "<h3>商品交易说明</h3><ol><li><p><strong>资料内容</strong><br>以word文档提供</p></li></ol>"},
		{"去掉全部属性", `<p class="x" style="color:red" onclick="alert(1)">文字</p>`, "<p>文字</p>"},
		{"脚本连同内容删除", `<p>前</p><script>alert(1)</script><p>后</p>`, "<p>前</p><p>后</p>"},
		{"样式与 iframe 删除", `<style>p{}</style><iframe src="https://evil"></iframe><p>正文</p>`, "<p>正文</p>"},
		{"图片与 onerror 删除", `<p>图<img src=x onerror="alert(1)"></p>`, "<p>图</p>"},
		{"链接去标签留文字", `<p><a href="javascript:alert(1)">点我</a></p>`, "<p>点我</p>"},
		{"未列入白名单的容器去标签留内容", `<div><span>甲</span><p>乙</p></div>`, "甲<p>乙</p>"},
		{"h1 降级为 h2", "<h1>标题</h1>", "<h2>标题</h2>"},
		{"文字中的尖括号重新转义", "<p>&lt;script&gt;alert(1)&lt;/script&gt; &amp; 3 &gt; 2</p>", "<p>&lt;script&gt;alert(1)&lt;/script&gt; &amp; 3 &gt; 2</p>"},
		{"纯文本也能输出", "以word文档提供各单元的知识点汇总", "以word文档提供各单元的知识点汇总"},
	}
	for _, c := range cases {
		if got := sanitizeProductContentHTML(c.raw); got != c.want {
			t.Errorf("%s：\n得到 %q\n期望 %q", c.name, got, c.want)
		}
	}
}

func TestBuildProductResourceInfoHTML(t *testing.T) {
	cases := []struct {
		name        string
		title       string
		fulfillment string
		want        string
	}{
		{
			name:        "单元课件",
			title:       "教科版小科六上第四单元《计量时间》单元课件",
			fulfillment: constants.FulfillmentTypeAuto,
			want:        `<section class="resource-info"><h2>资源包信息</h2><dl><dt>教材版本</dt><dd>教科版</dd><dt>年级册次</dt><dd>六年级上册</dd><dt>单元</dt><dd>第四单元《计量时间》</dd><dt>资源类型</dt><dd>单元课件</dd><dt>交付格式</dt><dd>PPT课件、教学设计</dd><dt>适配大屏设备</dt><dd>希沃</dd><dt>发货方式</dt><dd>付款后自动发货</dd></dl></section>`,
		},
		{
			name:        "未写单元号的单元课件",
			title:       "教科版小科五下《热》单元课件",
			fulfillment: constants.FulfillmentTypeAuto,
			want:        `<section class="resource-info"><h2>资源包信息</h2><dl><dt>教材版本</dt><dd>教科版</dd><dt>年级册次</dt><dd>五年级下册</dd><dt>单元</dt><dd>《热》</dd><dt>资源类型</dt><dd>单元课件</dd><dt>交付格式</dt><dd>PPT课件、教学设计</dd><dt>适配大屏设备</dt><dd>希沃</dd><dt>发货方式</dt><dd>付款后自动发货</dd></dl></section>`,
		},
		{
			name:        "知识手册写 Word，人工发货",
			title:       "苏教版小科3-6年级知识手册",
			fulfillment: constants.FulfillmentTypeManual,
			want:        `<section class="resource-info"><h2>资源包信息</h2><dl><dt>教材版本</dt><dd>苏教版</dd><dt>年级册次</dt><dd>3-6年级</dd><dt>资源类型</dt><dd>知识手册</dd><dt>交付格式</dt><dd>Word文档</dd><dt>发货方式</dt><dd>人工发货</dd></dl></section>`,
		},
		{
			name:        "无法解析的商品不输出",
			title:       "简易电路仿真实验工具台",
			fulfillment: constants.FulfillmentTypeAuto,
			want:        "",
		},
	}
	for _, c := range cases {
		if got := buildProductResourceInfoHTML(c.title, c.fulfillment); got != c.want {
			t.Errorf("%s：\n得到 %q\n期望 %q", c.name, got, c.want)
		}
	}
}

func TestBuildProductSpecListHTML(t *testing.T) {
	skus := []models.ProductSKU{
		{SpecValuesJSON: models.JSON{"zh-CN": "第1课时_声音是怎样产生的"}, IsActive: true},
		{SpecValuesJSON: models.JSON{"zh-CN": "第2课时_声音的强弱"}, IsActive: true},
		{SpecValuesJSON: models.JSON{"zh-CN": "第2课时_声音的强弱"}, IsActive: true},
		{SpecValuesJSON: models.JSON{"zh-CN": "已停用规格"}, IsActive: false},
		{SpecValuesJSON: models.JSON{"zh-CN": "<b>单元整套</b>打包价"}, IsActive: true},
		{IsActive: true},
	}
	want := `<section class="specs"><h2>可选规格</h2><ul><li>第1课时_声音是怎样产生的</li><li>第2课时_声音的强弱</li><li>&lt;b&gt;单元整套&lt;/b&gt;打包价</li></ul></section>`
	if got := buildProductSpecListHTML(skus); got != want {
		t.Errorf("\n得到 %q\n期望 %q", got, want)
	}
	if got := buildProductSpecListHTML(nil); got != "" {
		t.Errorf("无 SKU 时应为空，得到 %q", got)
	}
	if !strings.HasPrefix(buildProductSpecListHTML(skus[:1]), `<section class="specs">`) {
		t.Errorf("单个 SKU 也应输出规格清单")
	}
}
