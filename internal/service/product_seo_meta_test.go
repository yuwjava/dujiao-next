package service

import (
	"strings"
	"testing"

	"github.com/dujiao-next/internal/models"
)

func TestResolveProductPageSEO(t *testing.T) {
	cases := []struct {
		name      string
		product   *models.Product
		wantTitle string
		wantDesc  string
	}{
		{
			name:      "全走兜底",
			product:   &models.Product{Slug: "x", TitleJSON: models.JSON{"zh-CN": "苏教版小科3-6年级知识手册"}},
			wantTitle: "苏教版小学科学3-6年级知识手册｜备课箱",
			wantDesc:  "苏教版小学科学3-6年级知识手册，Word文档，按单元汇总知识点，适合课前预习与单元复习，可直接打印。扫码购买，自动发货。",
		},
		{
			name: "后台 seo_meta 的多语言 title 与 description 优先",
			product: &models.Product{
				Slug:      "y",
				TitleJSON: models.JSON{"zh-CN": "教科版小科五下《热》单元课件"},
				SeoMetaJSON: models.JSON{
					"title":       models.JSON{"zh-CN": "五年级下册《热》单元课件"},
					"description": models.JSON{"zh-CN": "五年级下册《热》单元课件，含温度与水的变化等 7 课时。"},
				},
			},
			wantTitle: "五年级下册《热》单元课件｜备课箱",
			wantDesc:  "五年级下册《热》单元课件，含温度与水的变化等 7 课时。",
		},
		{
			name:      "无法解析时退回商品短描述",
			product:   &models.Product{Slug: "z", TitleJSON: models.JSON{"zh-CN": "教科智慧教学平台视频批量下载器"}, DescriptionJSON: models.JSON{"zh-CN": "教科智慧教学平台视频批量下载器，支持单个和批量下载"}},
			wantTitle: "教科智慧教学平台视频批量下载器｜备课箱小学科学",
			wantDesc:  "教科智慧教学平台视频批量下载器，支持单个和批量下载",
		},
	}
	for _, c := range cases {
		title, desc := ResolveProductPageSEO(c.product)
		if title != c.wantTitle || desc != c.wantDesc {
			t.Errorf("%s：\n得到 %q / %q\n期望 %q / %q", c.name, title, desc, c.wantTitle, c.wantDesc)
		}
	}
	if title, desc := ResolveProductPageSEO(nil); title != "" || desc != "" {
		t.Errorf("nil 商品应返回空")
	}
}

func TestParseProductTitle(t *testing.T) {
	cases := []struct {
		title string
		ok    bool
		want  parsedProductTitle
	}{
		{"教科版小科六上第四单元《计量时间》单元课件", true, parsedProductTitle{Kind: productKindUnitCourseware, Version: "教科版", Grade: "六", Term: "上", Unit: "四", Lesson: "计量时间"}},
		{"苏教版小科一上第一单元《走进科学》单元课件", true, parsedProductTitle{Kind: productKindUnitCourseware, Version: "苏教版", Grade: "一", Term: "上", Unit: "一", Lesson: "走进科学"}},
		{"苏教版小科四下《地球、月球与太阳》单元课件", true, parsedProductTitle{Kind: productKindUnitCourseware, Version: "苏教版", Grade: "四", Term: "下", Lesson: "地球、月球与太阳"}},
		{"粤教版小科三下《抗风暴房屋》单元课件", true, parsedProductTitle{Kind: productKindUnitCourseware, Version: "粤教版", Grade: "三", Term: "下", Lesson: "抗风暴房屋"}},
		{"  教科版小科五下《热》单元课件  ", true, parsedProductTitle{Kind: productKindUnitCourseware, Version: "教科版", Grade: "五", Term: "下", Lesson: "热"}},
		{"教科版小科3-6年级知识手册", true, parsedProductTitle{Kind: productKindHandbook, Version: "教科版", Grade: "3-6"}},
		{"苏教版小科1-6年级《开学第一课》（起始课）", true, parsedProductTitle{Kind: productKindStarterLesson, Version: "苏教版", Grade: "1-6", Lesson: "开学第一课"}},
		{"教科智慧教学平台视频批量下载器", false, parsedProductTitle{}},
		{"简易电路仿真实验工具台", false, parsedProductTitle{}},
		{"免费PPT模板更多风格持续更新中", false, parsedProductTitle{}},
		{"教科版小科七上《不存在》单元课件", false, parsedProductTitle{}},
		{"", false, parsedProductTitle{}},
	}
	for _, c := range cases {
		got, ok := parseProductTitle(c.title)
		if ok != c.ok || got != c.want {
			t.Errorf("parseProductTitle(%q) = %+v, %v；期望 %+v, %v", c.title, got, ok, c.want, c.ok)
		}
	}
}

func TestBuildProductPageTitle(t *testing.T) {
	cases := []struct {
		seoTitle string
		title    string
		want     string
	}{
		{"", "教科版小科六上第四单元《计量时间》单元课件", "教科版小学科学六年级上册 第四单元《计量时间》单元课件｜备课箱"},
		{"", "苏教版小科五下《仿生》单元课件", "苏教版小学科学五年级下册《仿生》单元课件｜备课箱"},
		{"", "教科版小科3-6年级知识手册", "教科版小学科学3-6年级知识手册｜备课箱"},
		{"", "教科版小科1-6年级《开学第一课》（起始课）", "教科版小学科学1-6年级《开学第一课》起始课｜备课箱"},
		{"", "简易电路仿真实验工具台", "简易电路仿真实验工具台｜备课箱小学科学"},
		// 后台 seo_meta.title 优先
		{"计量时间单元课件 六年级科学", "教科版小科六上第四单元《计量时间》单元课件", "计量时间单元课件 六年级科学｜备课箱"},
		{"计量时间单元课件｜备课箱", "教科版小科六上第四单元《计量时间》单元课件", "计量时间单元课件｜备课箱"},
	}
	for _, c := range cases {
		if got := BuildProductPageTitle(c.seoTitle, c.title); got != c.want {
			t.Errorf("BuildProductPageTitle(%q, %q) = %q；期望 %q", c.seoTitle, c.title, got, c.want)
		}
	}
}

func TestBuildStructuredProductDescription(t *testing.T) {
	cases := []struct {
		name      string
		seoDesc   string
		title     string
		shortDesc string
		want      string
	}{
		{
			name:  "单元课件按解析结果套模板，版本只出现一次",
			title: "教科版小科六上第四单元《计量时间》单元课件",
			want:  "教科版小学科学六年级上册第四单元《计量时间》单元课件，含课件PPT与教学设计，部分版本另含练习题、逐字稿与讲解视频。AI辅助生成，教研组三审三校；扫码购买，自动发货。",
		},
		{
			name:  "知识手册写 Word 文档，不写 PPT",
			title: "苏教版小科3-6年级知识手册",
			want:  "苏教版小学科学3-6年级知识手册，Word文档，按单元汇总知识点，适合课前预习与单元复习，可直接打印。扫码购买，自动发货。",
		},
		{
			name:    "后台 seo_meta.description 有效时优先",
			seoDesc: "六年级上册《计量时间》单元课件，含摆的研究、机械摆钟等 7 课。",
			title:   "教科版小科六上第四单元《计量时间》单元课件",
			want:    "六年级上册《计量时间》单元课件，含摆的研究、机械摆钟等 7 课。",
		},
		{
			name:    "seo_meta.description 过短时仍走兜底",
			seoDesc: "计量时间",
			title:   "教科版小科五下《热》单元课件",
			want:    "教科版小学科学五年级下册《热》单元课件，含课件PPT与教学设计，部分版本另含练习题、逐字稿与讲解视频。AI辅助生成，教研组三审三校；扫码购买，自动发货。",
		},
		{
			name:      "无法解析的商品用商品短描述",
			title:     "教科智慧教学平台视频批量下载器",
			shortDesc: "教科智慧教学平台视频批量下载器，支持单个和批量下载",
			want:      "教科智慧教学平台视频批量下载器，支持单个和批量下载",
		},
		{
			name:      "无法解析且短描述过短时用通用句",
			title:     "简易电路仿真实验工具台",
			shortDesc: "简易电路仿真实验工具台",
			want:      "简易电路仿真实验工具台，备课箱小学科学备课商城在售资源。",
		},
	}
	for _, c := range cases {
		if got := BuildStructuredProductDescription(c.seoDesc, c.title, c.shortDesc); got != c.want {
			t.Errorf("%s：\n得到 %q\n期望 %q", c.name, got, c.want)
		}
	}
}

// 线上商城 2026-10-31 全部 65 个商品展示名快照
var shopProductTitlesSnapshot = []string{
	"免费PPT模板更多风格持续更新中",
	"教科版小科3-6年级知识手册",
	"苏教版小科3-6年级知识手册",
	"教科智慧教学平台视频批量下载器",
	"教科版小科六上第四单元《计量时间》单元课件",
	"教科版小科六上第二单元《光》单元课件",
	"教科版小科五上第四单元《地球表面的变化》单元课件",
	"教科版小科五上第三单元《运动和力》单元课件",
	"教科版小科一上第二单元《我们自己》单元课件",
	"教科版小科三上第三单元《物体的运动》单元课件",
	"教科版小科四上第三单元《声音》单元课件",
	"教科版小科二上第二单元《地球家园》单元课件",
	"教科版小科三上第二单元《水》单元课件",
	"教科版小科四上第二单元《呼吸与消化》单元课件",
	"教科版小科五上第二单元《工具与技术》单元课件",
	"苏教版小科一上第一单元《走进科学》单元课件",
	"苏教版小科二上第一单元《它们是用什么做的》单元课件",
	"苏教版小科三上第一单元《研究土壤》单元课件",
	"苏教版小科四上第一单元《动物大家族》单元课件",
	"苏教版小科五上第一单元《光与色彩》单元课件",
	"苏教版小科六上第一单元《物质的变化》单元课件",
	"教科版小科六上第三单元《地球的运动》单元课件",
	"教科版小科三上第一单元《天气》单元课件",
	"教科版小科二上第一单元《动物的家》单元课件",
	"教科版小科六上第一单元《健康生活》单元课件",
	"教科版小科五上第一单元《微小世界》单元课件",
	"教科版小科四上第一单元《空气》单元课件",
	"教科版小科一上第一单元《周围的植物》单元课件",
	"苏教版小科1-6年级《开学第一课》（起始课）",
	"教科版小科1-6年级《开学第一课》（起始课）",
	"苏教版小科五下《显微镜下的生命世界》单元课件",
	"苏教版小科五下《仿生》单元课件",
	"苏教版小科四下《冷和热》单元课件",
	"苏教版小科四下《地球、月球与太阳》单元课件",
	"苏教版小科四下《昆虫》单元课件",
	"粤教版小科三下《观测天气》单元课件",
	"粤教版小科三下《抗风暴房屋》单元课件",
	"粤教版小科四下《运动与力》单元课件",
	"粤教版小科四下《地球上看到的光和影》单元课件",
	"粤教版小科五下《火山与地震》单元课件",
	"粤教版小科五下《地球运动与宇宙》单元课件",
	"粤教版小科六下《动物与环境》单元课件",
	"粤教版小科六下《我们的家园》单元课件",
	"苏教版小科六下《自然资源》单元课件",
	"苏教版小科六下《理想的家园》单元课件",
	"教科版小科五下《船的研究》单元课件",
	"苏教版小科五下《地球的运动》单元课件",
	"苏教版小科五下《简单机械》单元课件",
	"教科版小科二下《玩磁铁》单元课件",
	"教科版小科三下《辨别方向》单元课件",
	"教科版小科三下《动物的一生》单元课件",
	"教科版小科六下《小小工程师》单元课件",
	"教科版小科六下《生物的多样性》单元课件",
	"教科版小科六下《宇宙》单元课件",
	"教科版小科三下《只有一个地球》单元课件",
	"教科版小科五下《环境与我们》单元课件",
	"苏教版小科四下《繁殖》单元课件",
	"苏教版小科四下《生物与环境》单元课件",
	"教科版小科六下《物质的变化》单元课件",
	"简易电路仿真实验工具台",
	"教科版小科五下《热》单元课件",
	"教科版小科五下《生物与环境》单元课件",
	"教科版小科四下《植物的生长变化》单元课件",
	"教科版小科四下《电路》单元课件",
	"教科版小科四下《岩石与土壤》单元课件",
}

// 对应 #7 验收：全部标题含“小学科学”、描述两两不同、版本前缀不重复、知识手册不出现 PPT
func TestProductSEOFallbackOverShopCatalog(t *testing.T) {
	if len(shopProductTitlesSnapshot) != 65 {
		t.Fatalf("快照应为 65 个商品，实际 %d", len(shopProductTitlesSnapshot))
	}

	seenDesc := map[string]string{}
	unparsed := 0
	for _, title := range shopProductTitlesSnapshot {
		pageTitle := BuildProductPageTitle("", title)
		desc := BuildStructuredProductDescription("", title, "")

		if !strings.Contains(pageTitle, "小学科学") {
			t.Errorf("%q 的 <title> 不含“小学科学”：%q", title, pageTitle)
		}
		if prev, dup := seenDesc[desc]; dup {
			t.Errorf("描述重复：%q 与 %q 都是 %q", prev, title, desc)
		}
		seenDesc[desc] = title

		p, ok := parseProductTitle(title)
		if !ok {
			unparsed++
			continue
		}
		if n := strings.Count(desc, p.Version); n != 1 {
			t.Errorf("%q 的描述里版本 %q 出现 %d 次：%q", title, p.Version, n, desc)
		}
		if p.Kind == productKindHandbook && strings.Contains(desc, "PPT") {
			t.Errorf("知识手册 %q 的描述不应出现 PPT：%q", title, desc)
		}
	}
	// 只有免费 PPT 模板、视频下载器、电路仿真工具台三个商品不属于教材同步资源
	if unparsed != 3 {
		t.Errorf("期望 3 个商品走通用兜底，实际 %d", unparsed)
	}
}
