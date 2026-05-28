@echo off
chcp 65001 > nul
echo ==========================================
echo  Dujiao-Next API 编译脚本 (单独 API 模式)
echo ==========================================

set CGO_ENABLED=0
set APP_VERSION=v1.0.0

if not exist bin mkdir bin

echo 请选择编译目标:
echo [1] 编译为 Linux (amd64)
echo [2] 编译为 Windows (amd64)
set /p opt="请输入数字 (1 或 2): "

if "%opt%"=="1" goto build_linux
if "%opt%"=="2" goto build_windows
echo 输入无效，退出。
goto end

:build_linux
echo 正在编译 Linux (amd64) 版本...
set GOOS=linux
set GOARCH=amd64
go build -trimpath -tags release -ldflags="-s -w -X github.com/dujiao-next/internal/version.Version=%APP_VERSION%" -o bin/dujiao-api ./cmd/server
if %errorlevel% equ 0 (
    echo [成功] 编译完成，产物已输出至: bin/dujiao-api
) else (
    echo [失败] 编译出错，请检查错误信息。
)
goto end

:build_windows
echo 正在编译 Windows (amd64) 版本...
set GOOS=windows
set GOARCH=amd64
go build -trimpath -tags release -ldflags="-s -w -X github.com/dujiao-next/internal/version.Version=%APP_VERSION%" -o bin/dujiao-api.exe ./cmd/server
if %errorlevel% equ 0 (
    echo [成功] 编译完成，产物已输出至: bin/dujiao-api.exe
) else (
    echo [失败] 编译出错，请检查错误信息。
)
goto end

:end
pause
