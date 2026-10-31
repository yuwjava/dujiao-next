package service

import (
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/dujiao-next/internal/logger"
)

// responsiveWebPWidths 上传图片额外派生的宽度档，文件名为 <原图>.w{宽}.webp。
// 须与 user/src/utils/image.ts 的 RESPONSIVE_IMAGE_WIDTHS 保持一致；
// nginx 在某档文件缺失时回退到原图（见 deploy/nginx/extension/shop.beikevip.site）。
var responsiveWebPWidths = []int{480, 960, 1440}

// errCwebpNotFound 系统未安装 cwebp（生产镜像通过 libwebp-tools 提供）
var errCwebpNotFound = errors.New("cwebp not found")

type webpDerivative struct {
	Path        string
	ResizeWidth int // 0 表示按原尺寸转换
}

// webpDerivativesFor 原图需要的全部 WebP 派生文件：同尺寸副本 <原图>.webp，以及各宽度档 <原图>.w{宽}.webp。
// 原图不比某档宽（或宽度未知）时不放大，按原尺寸转换；文件照常生成，保证前台 srcset 的每一档都有文件。
func webpDerivativesFor(savePath string, srcWidth int) []webpDerivative {
	list := []webpDerivative{{Path: savePath + ".webp"}}
	for _, w := range responsiveWebPWidths {
		resize := 0
		if srcWidth > w {
			resize = w
		}
		list = append(list, webpDerivative{Path: fmt.Sprintf("%s.w%d.webp", savePath, w), ResizeWidth: resize})
	}
	return list
}

// cwebpArgs 生成一次 cwebp 调用的参数
func cwebpArgs(src string, d webpDerivative) []string {
	args := []string{"-q", "82", "-mt"}
	if d.ResizeWidth > 0 {
		args = append(args, "-resize", strconv.Itoa(d.ResizeWidth), "0")
	}
	return append(args, src, "-o", d.Path)
}

// WebPDerivativeStats 一次派生的结果计数
type WebPDerivativeStats struct {
	Generated int
	Skipped   int // skipExisting 时已存在而跳过的文件
	Failed    int
}

func (a *WebPDerivativeStats) add(b WebPDerivativeStats) {
	a.Generated += b.Generated
	a.Skipped += b.Skipped
	a.Failed += b.Failed
}

// GenerateWebPDerivatives 为一张原图生成全部 WebP 派生文件。
// skipExisting 为 true 时跳过已存在的文件（补齐旧图用）；单个文件失败只记日志并计入 Failed，不中断其余档位。
// 系统没有 cwebp 时返回 errCwebpNotFound。
func (s *UploadService) GenerateWebPDerivatives(savePath string, srcWidth int, skipExisting bool) (WebPDerivativeStats, error) {
	var stats WebPDerivativeStats

	lookPath := s.cwebpLookPath
	if lookPath == nil {
		lookPath = exec.LookPath
	}
	cwebpBin, err := lookPath("cwebp")
	if err != nil || strings.TrimSpace(cwebpBin) == "" {
		return stats, errCwebpNotFound
	}

	runner := s.cwebpRunner
	if runner == nil {
		runner = func(ctx context.Context, bin string, args ...string) ([]byte, error) {
			return exec.CommandContext(ctx, bin, args...).CombinedOutput()
		}
	}

	for _, d := range webpDerivativesFor(savePath, srcWidth) {
		if skipExisting {
			if _, statErr := os.Stat(d.Path); statErr == nil {
				stats.Skipped++
				continue
			}
		}
		ctx, cancel := context.WithTimeout(context.Background(), webpSidecarTimeout)
		out, runErr := runner(ctx, cwebpBin, cwebpArgs(savePath, d)...)
		cancel()
		if runErr != nil {
			stats.Failed++
			logger.Warnw("upload_generate_webp_failed",
				"source", savePath,
				"target", d.Path,
				"error", runErr,
				"output", strings.TrimSpace(string(out)),
			)
			continue
		}
		stats.Generated++
	}
	return stats, nil
}

// BackfillWebPDerivatives 遍历 root 下全部 png/jpg/jpeg 原图，补齐缺失的 WebP 派生文件（已存在的不重做）。
// dryRun 为 true 时只统计需要补齐的原图数，不调用 cwebp。进度写入 out。
func (s *UploadService) BackfillWebPDerivatives(root string, dryRun bool, out io.Writer) (images int, stats WebPDerivativeStats, err error) {
	walkErr := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() || !shouldGenerateWebPSidecar(filepath.Ext(path)) {
			return nil
		}

		missing := 0
		width := imageFileWidth(path)
		for _, d := range webpDerivativesFor(path, width) {
			if _, statErr := os.Stat(d.Path); statErr != nil {
				missing++
			}
		}
		if missing == 0 {
			return nil
		}
		images++

		if dryRun {
			fmt.Fprintf(out, "[待补齐] %s（宽 %d，缺 %d 个文件）\n", path, width, missing)
			stats.Skipped += missing
			return nil
		}
		one, genErr := s.GenerateWebPDerivatives(path, width, true)
		if genErr != nil {
			return genErr
		}
		stats.add(one)
		fmt.Fprintf(out, "[已处理] %s：生成 %d，失败 %d\n", path, one.Generated, one.Failed)
		return nil
	})
	return images, stats, walkErr
}

// imageFileWidth 读取图片宽度；无法解析时返回 0（按原尺寸转换，不放大）
func imageFileWidth(path string) int {
	f, err := os.Open(path)
	if err != nil {
		return 0
	}
	defer f.Close()
	cfg, _, err := image.DecodeConfig(f)
	if err != nil {
		return 0
	}
	return cfg.Width
}
