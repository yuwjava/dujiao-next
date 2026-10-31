package service

import (
	"bytes"
	"context"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestWebPDerivativesFor(t *testing.T) {
	got := webpDerivativesFor("u/a.png", 1536)
	want := []webpDerivative{
		{Path: "u/a.png.webp"},
		{Path: "u/a.png.w480.webp", ResizeWidth: 480},
		{Path: "u/a.png.w960.webp", ResizeWidth: 960},
		{Path: "u/a.png.w1440.webp", ResizeWidth: 1440},
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("宽 1536：得到 %+v，期望 %+v", got, want)
	}

	// 原图只有 800 宽：480 档缩小，960、1440 档不放大
	got = webpDerivativesFor("u/b.jpg", 800)
	if got[1].ResizeWidth != 480 || got[2].ResizeWidth != 0 || got[3].ResizeWidth != 0 {
		t.Errorf("宽 800：各档缩放应为 480/0/0，得到 %+v", got)
	}

	// 宽度未知：一律不缩放
	for _, d := range webpDerivativesFor("u/c.png", 0) {
		if d.ResizeWidth != 0 {
			t.Errorf("宽度未知时不应缩放：%+v", d)
		}
	}
}

func TestCwebpArgs(t *testing.T) {
	if got := cwebpArgs("a.png", webpDerivative{Path: "a.png.w480.webp", ResizeWidth: 480}); !reflect.DeepEqual(got,
		[]string{"-q", "82", "-mt", "-resize", "480", "0", "a.png", "-o", "a.png.w480.webp"}) {
		t.Errorf("带缩放参数不符：%v", got)
	}
	if got := cwebpArgs("a.png", webpDerivative{Path: "a.png.webp"}); !reflect.DeepEqual(got,
		[]string{"-q", "82", "-mt", "a.png", "-o", "a.png.webp"}) {
		t.Errorf("不缩放参数不符：%v", got)
	}
}

func writeTestPNG(t *testing.T, path string, width int) {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, width, 4))); err != nil {
		t.Fatalf("编码 PNG 失败: %v", err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
}

func newMockCwebpService(calls *[][]string, failTarget string) *UploadService {
	svc := NewUploadService(nil)
	svc.cwebpLookPath = func(string) (string, error) { return "/usr/bin/cwebp", nil }
	svc.cwebpRunner = func(ctx context.Context, bin string, args ...string) ([]byte, error) {
		*calls = append(*calls, append([]string(nil), args...))
		target := args[len(args)-1]
		if failTarget != "" && strings.HasSuffix(target, failTarget) {
			return []byte("encoding error"), os.ErrInvalid
		}
		return nil, os.WriteFile(target, []byte("RIFFmockWEBP"), 0o644)
	}
	return svc
}

func TestBackfillWebPDerivatives(t *testing.T) {
	root := t.TempDir()
	wide := filepath.Join(root, "product", "2026", "10", "wide.png")
	done := filepath.Join(root, "product", "2026", "10", "done.png")
	writeTestPNG(t, wide, 1536)
	writeTestPNG(t, done, 300)
	// wide.png 已有同尺寸副本（旧逻辑生成），只缺三档
	if err := os.WriteFile(wide+".webp", []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	// done.png 全部派生文件都已存在
	for _, d := range webpDerivativesFor(done, 300) {
		if err := os.WriteFile(d.Path, []byte("x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// 非图片与已有 webp 原图不处理
	if err := os.WriteFile(filepath.Join(root, "readme.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("dry-run 只统计、不调用 cwebp", func(t *testing.T) {
		var calls [][]string
		svc := newMockCwebpService(&calls, "")
		var out bytes.Buffer
		images, stats, err := svc.BackfillWebPDerivatives(root, true, &out)
		if err != nil {
			t.Fatalf("dry-run 失败: %v", err)
		}
		if images != 1 || stats.Skipped != 3 || len(calls) != 0 {
			t.Errorf("dry-run 期望 1 张待补齐、缺 3 个文件、0 次调用，得到 %d / %+v / %d 次\n%s", images, stats, len(calls), out.String())
		}
	})

	t.Run("补齐缺失档位，已存在的不重做，单档失败不中断", func(t *testing.T) {
		var calls [][]string
		svc := newMockCwebpService(&calls, ".w960.webp")
		var out bytes.Buffer
		images, stats, err := svc.BackfillWebPDerivatives(root, false, &out)
		if err != nil {
			t.Fatalf("补齐失败: %v", err)
		}
		if images != 1 {
			t.Errorf("应只处理 wide.png，得到 %d 张\n%s", images, out.String())
		}
		if stats.Generated != 2 || stats.Failed != 1 || stats.Skipped != 1 {
			t.Errorf("期望生成 2、失败 1、跳过 1（已有 .webp），得到 %+v", stats)
		}
		if len(calls) != 3 {
			t.Fatalf("期望调用 3 次 cwebp，得到 %d：%v", len(calls), calls)
		}
		if !reflect.DeepEqual(calls[0], []string{"-q", "82", "-mt", "-resize", "480", "0", wide, "-o", wide + ".w480.webp"}) {
			t.Errorf("480 档参数不符：%v", calls[0])
		}
		if b, _ := os.ReadFile(wide + ".webp"); string(b) != "old" {
			t.Errorf("已存在的同尺寸副本不应被覆盖")
		}
	})

	t.Run("没有 cwebp 时返回错误", func(t *testing.T) {
		svc := NewUploadService(nil)
		svc.cwebpLookPath = func(string) (string, error) { return "", os.ErrNotExist }
		if _, _, err := svc.BackfillWebPDerivatives(root, false, &bytes.Buffer{}); err == nil {
			t.Errorf("没有 cwebp 时应返回错误")
		}
	})
}
