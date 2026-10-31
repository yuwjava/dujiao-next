package service

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"mime/multipart"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/dujiao-next/internal/config"
)

func TestUploadServiceSaveFileAllowsArchiveForTelegramScene(t *testing.T) {
	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd failed: %v", err)
	}
	tempDir := t.TempDir()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("chdir temp dir failed: %v", err)
	}
	defer func() {
		_ = os.Chdir(originalWD)
	}()

	cfg := &config.Config{}
	cfg.Upload.MaxSize = 10 * 1024 * 1024
	cfg.Upload.AllowedTypes = []string{"image/jpeg", "image/png"}
	cfg.Upload.AllowedExtensions = []string{".jpg", ".png"}
	service := NewUploadService(cfg)

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", "demo.zip")
	if err != nil {
		t.Fatalf("create form file failed: %v", err)
	}
	if _, err := part.Write([]byte("fake zip content")); err != nil {
		t.Fatalf("write form content failed: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer failed: %v", err)
	}

	reader := multipart.NewReader(&body, writer.Boundary())
	form, err := reader.ReadForm(1024 * 1024)
	if err != nil {
		t.Fatalf("read form failed: %v", err)
	}
	files := form.File["file"]
	if len(files) != 1 {
		t.Fatalf("expected one file, got %d", len(files))
	}

	savedPath, err := service.SaveFile(files[0], "telegram")
	if err != nil {
		t.Fatalf("save file failed: %v", err)
	}
	if filepath.Ext(savedPath) != ".zip" {
		t.Fatalf("expected .zip saved path, got %s", savedPath)
	}
	if _, err := os.Stat(filepath.Join(tempDir, strings.TrimPrefix(savedPath, "/"))); err != nil {
		t.Fatalf("saved file not found: %v", err)
	}
}

func TestUploadServiceSaveFileSVG(t *testing.T) {
	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd failed: %v", err)
	}
	tempDir := t.TempDir()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("chdir temp dir failed: %v", err)
	}
	defer func() {
		_ = os.Chdir(originalWD)
	}()

	cfg := &config.Config{}
	cfg.Upload.MaxSize = 10 * 1024 * 1024
	cfg.Upload.AllowedTypes = []string{"image/jpeg", "image/png", "image/svg+xml"}
	cfg.Upload.AllowedExtensions = []string{".jpg", ".png", ".svg"}
	svc := NewUploadService(cfg)

	safeSVG := `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100"><circle cx="50" cy="50" r="40" fill="red"/></svg>`

	t.Run("safe SVG upload succeeds", func(t *testing.T) {
		fh := createMultipartFile(t, "icon.svg", []byte(safeSVG))
		path, err := svc.SaveFile(fh, "common")
		if err != nil {
			t.Fatalf("expected success, got error: %v", err)
		}
		if filepath.Ext(path) != ".svg" {
			t.Fatalf("expected .svg extension, got %s", path)
		}
		if _, err := os.Stat(filepath.Join(tempDir, strings.TrimPrefix(path, "/"))); err != nil {
			t.Fatalf("saved file not found: %v", err)
		}
	})

	t.Run("SVG with script tag is rejected", func(t *testing.T) {
		malicious := `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`
		fh := createMultipartFile(t, "bad.svg", []byte(malicious))
		_, err := svc.SaveFile(fh, "common")
		if err == nil {
			t.Fatal("expected error for SVG with script tag")
		}
		if !strings.Contains(err.Error(), "<script>") {
			t.Fatalf("unexpected error message: %v", err)
		}
	})

	t.Run("SVG with event handler is rejected", func(t *testing.T) {
		malicious := `<svg xmlns="http://www.w3.org/2000/svg" onload="alert(1)"><circle cx="50" cy="50" r="40"/></svg>`
		fh := createMultipartFile(t, "bad2.svg", []byte(malicious))
		_, err := svc.SaveFile(fh, "common")
		if err == nil {
			t.Fatal("expected error for SVG with event handler")
		}
		if !strings.Contains(err.Error(), "onload") {
			t.Fatalf("unexpected error message: %v", err)
		}
	})

	t.Run("SVG with javascript protocol is rejected", func(t *testing.T) {
		malicious := `<svg xmlns="http://www.w3.org/2000/svg"><a href="javascript:alert(1)"><circle cx="50" cy="50" r="40"/></a></svg>`
		fh := createMultipartFile(t, "bad3.svg", []byte(malicious))
		_, err := svc.SaveFile(fh, "common")
		if err == nil {
			t.Fatal("expected error for SVG with javascript protocol")
		}
	})

	t.Run("SVG with foreignObject is rejected", func(t *testing.T) {
		malicious := `<svg xmlns="http://www.w3.org/2000/svg"><foreignObject><body xmlns="http://www.w3.org/1999/xhtml"><div>hello</div></body></foreignObject></svg>`
		fh := createMultipartFile(t, "bad4.svg", []byte(malicious))
		_, err := svc.SaveFile(fh, "common")
		if err == nil {
			t.Fatal("expected error for SVG with foreignObject")
		}
	})

	t.Run("SVG with XML declaration", func(t *testing.T) {
		xmlSVG := `<?xml version="1.0" encoding="UTF-8"?><svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100"><rect width="100" height="100" fill="blue"/></svg>`
		fh := createMultipartFile(t, "xml.svg", []byte(xmlSVG))
		path, err := svc.SaveFile(fh, "common")
		if err != nil {
			t.Fatalf("expected success, got error: %v", err)
		}
		if filepath.Ext(path) != ".svg" {
			t.Fatalf("expected .svg extension, got %s", path)
		}
	})
}

func TestIsSVGContent(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		expect bool
	}{
		{"svg tag", `<svg xmlns="http://www.w3.org/2000/svg"></svg>`, true},
		{"xml declaration", `<?xml version="1.0"?><svg></svg>`, true},
		{"not svg", `<html><body></body></html>`, false},
		{"plain text", `hello world`, false},
		{"empty", ``, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isSVGContent([]byte(tt.input))
			if got != tt.expect {
				t.Errorf("isSVGContent(%q) = %v, want %v", tt.input, got, tt.expect)
			}
		})
	}
}

func TestValidateSVGSafety(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{"safe svg", `<svg xmlns="http://www.w3.org/2000/svg"><circle cx="50" cy="50" r="40"/></svg>`, false},
		{"script tag", `<svg><script>alert(1)</script></svg>`, true},
		{"onclick", `<svg onclick="alert(1)"></svg>`, true},
		{"javascript href", `<svg><a href="javascript:void(0)"></a></svg>`, true},
		{"data uri html", `<svg><image href="data:text/html,<h1>hi</h1>"/></svg>`, true},
		{"foreignObject", `<svg><foreignObject></foreignObject></svg>`, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateSVGSafety([]byte(tt.input))
			if (err != nil) != tt.wantErr {
				t.Errorf("validateSVGSafety() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestUploadServiceWebPSidecarGeneration(t *testing.T) {
	originalWD, err := os.Getwd()
	if err != nil {
		t.Fatalf("获取当前工作目录失败: %v", err)
	}
	tempDir := t.TempDir()
	if err := os.Chdir(tempDir); err != nil {
		t.Fatalf("切换临时工作目录失败: %v", err)
	}
	defer func() {
		_ = os.Chdir(originalWD)
	}()

	cfg := &config.Config{}
	cfg.Upload.MaxSize = 10 * 1024 * 1024
	cfg.Upload.AllowedTypes = []string{"image/jpeg", "image/png"}
	cfg.Upload.AllowedExtensions = []string{".jpg", ".jpeg", ".png"}

	pngBytes := encodeSamplePNG(t)
	jpgBytes := encodeSampleJPEG(t)

	t.Run("存在 cwebp 时旁路生成同名 .webp 副本与三档宽度", func(t *testing.T) {
		svc := NewUploadService(cfg)
		var calls [][]string
		svc.cwebpLookPath = func(file string) (string, error) {
			return "/usr/bin/cwebp", nil
		}
		svc.cwebpRunner = func(ctx context.Context, bin string, args ...string) ([]byte, error) {
			calls = append(calls, append([]string(nil), args...))
			// args 格式: -q 82 -mt [-resize W 0] <savePath> -o <目标>
			target := args[len(args)-1]
			if writeErr := os.WriteFile(target, []byte("RIFFmockWEBP"), 0644); writeErr != nil {
				return nil, writeErr
			}
			return []byte("ok"), nil
		}

		fh := createMultipartFile(t, "banner.png", pngBytes)
		savedURL, saveErr := svc.SaveFile(fh, "banner")
		if saveErr != nil {
			t.Fatalf("保存 PNG 图片失败: %v", saveErr)
		}

		localOrig := filepath.FromSlash(strings.TrimPrefix(savedURL, "/"))
		localWebP := localOrig + ".webp"
		if _, statErr := os.Stat(localOrig); statErr != nil {
			t.Fatalf("原图未落盘: %v", statErr)
		}
		if _, statErr := os.Stat(localWebP); statErr != nil {
			t.Fatalf("旁路 .webp 副本未生成: %v", statErr)
		}

		// 样例图很小，不比任何一档宽，所以各档都按原尺寸转换、不带 -resize
		expectedCalls := [][]string{
			{"-q", "82", "-mt", localOrig, "-o", localWebP},
			{"-q", "82", "-mt", localOrig, "-o", localOrig + ".w480.webp"},
			{"-q", "82", "-mt", localOrig, "-o", localOrig + ".w960.webp"},
			{"-q", "82", "-mt", localOrig, "-o", localOrig + ".w1440.webp"},
		}
		if !reflect.DeepEqual(calls, expectedCalls) {
			t.Fatalf("cwebp 调用参数不符，实际 %v，期望 %v", calls, expectedCalls)
		}
	})

	t.Run("开发环境无 cwebp 时静默跳过且不影响原图上传", func(t *testing.T) {
		svc := NewUploadService(cfg)
		runnerCalled := false
		svc.cwebpLookPath = func(file string) (string, error) {
			return "", errors.New("executable file not found in $PATH")
		}
		svc.cwebpRunner = func(ctx context.Context, bin string, args ...string) ([]byte, error) {
			runnerCalled = true
			return nil, nil
		}

		fh := createMultipartFile(t, "cover.jpg", jpgBytes)
		savedURL, saveErr := svc.SaveFile(fh, "product")
		if saveErr != nil {
			t.Fatalf("无 cwebp 时原图上传应成功，实际报错: %v", saveErr)
		}
		if runnerCalled {
			t.Fatalf("未找到 cwebp 时不应执行转换命令")
		}
		localOrig := filepath.FromSlash(strings.TrimPrefix(savedURL, "/"))
		if _, statErr := os.Stat(localOrig); statErr != nil {
			t.Fatalf("原图未落盘: %v", statErr)
		}
		if _, statErr := os.Stat(localOrig + ".webp"); !os.IsNotExist(statErr) {
			t.Fatalf("无 cwebp 时不应生成 .webp 文件")
		}
	})

	t.Run("cwebp 转换失败时仅记录日志且绝不阻断原图返回", func(t *testing.T) {
		svc := NewUploadService(cfg)
		svc.cwebpLookPath = func(file string) (string, error) {
			return "/usr/bin/cwebp", nil
		}
		svc.cwebpRunner = func(ctx context.Context, bin string, args ...string) ([]byte, error) {
			return []byte("encoding error"), errors.New("exit status 1")
		}

		fh := createMultipartFile(t, "photo.jpeg", jpgBytes)
		savedURL, saveErr := svc.SaveFile(fh, "product")
		if saveErr != nil {
			t.Fatalf("cwebp 报错不应阻断原图上传，实际报错: %v", saveErr)
		}
		localOrig := filepath.FromSlash(strings.TrimPrefix(savedURL, "/"))
		if _, statErr := os.Stat(localOrig); statErr != nil {
			t.Fatalf("原图未落盘: %v", statErr)
		}
	})
}

// encodeSamplePNG 生成合法最小 PNG 字节流供上传测试使用
func encodeSamplePNG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	img.Set(0, 0, color.RGBA{R: 16, G: 185, B: 129, A: 255})
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatalf("编码测试 PNG 失败: %v", err)
	}
	return buf.Bytes()
}

// encodeSampleJPEG 生成合法最小 JPEG 字节流供上传测试使用
func encodeSampleJPEG(t *testing.T) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, 8, 8))
	img.Set(0, 0, color.RGBA{R: 59, G: 130, B: 246, A: 255})
	var buf bytes.Buffer
	if err := jpeg.Encode(&buf, img, &jpeg.Options{Quality: 80}); err != nil {
		t.Fatalf("编码测试 JPEG 失败: %v", err)
	}
	return buf.Bytes()
}

// createMultipartFile 辅助函数：创建模拟的 multipart 文件
func createMultipartFile(t *testing.T, filename string, content []byte) *multipart.FileHeader {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	part, err := writer.CreateFormFile("file", filename)
	if err != nil {
		t.Fatalf("create form file failed: %v", err)
	}
	if _, err := part.Write(content); err != nil {
		t.Fatalf("write form content failed: %v", err)
	}
	if err := writer.Close(); err != nil {
		t.Fatalf("close writer failed: %v", err)
	}
	reader := multipart.NewReader(&body, writer.Boundary())
	form, err := reader.ReadForm(1024 * 1024)
	if err != nil {
		t.Fatalf("read form failed: %v", err)
	}
	files := form.File["file"]
	if len(files) != 1 {
		t.Fatalf("expected one file, got %d", len(files))
	}
	return files[0]
}
