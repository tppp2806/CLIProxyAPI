@echo off
chcp 65001 >nul
title CLIProxyAPI 重启工具

echo ==============================
echo    CLIProxyAPI 重启工具
echo ==============================
echo.

:: --- 第一步：关闭已运行的进程 ---
echo [1/3] 正在查找并关闭 cli-proxy-api.exe...
taskkill /F /IM cli-proxy-api.exe >nul 2>&1
if %errorlevel% neq 0 (
    echo      未找到正在运行的进程，跳过关闭步骤。
) else (
    echo      已发送关闭指令。
)

:: --- 第二步：等待进程完全退出 ---
echo [2/3] 正在等待进程完全退出...
:wait_loop
tasklist /FI "IMAGENAME eq cli-proxy-api.exe" 2>NUL | find /I /N "cli-proxy-api.exe">NUL
if %errorlevel%==0 (
    timeout /t 1 >nul
    echo      进程仍在退出中，请稍候...
    goto wait_loop
)
echo      旧进程已完全关闭。

:: --- 第三步：启动新进程 ---
echo [3/3] 正在启动新的 cli-proxy-api.exe...
cli-proxy-api.exe

echo.
echo ==============================
echo    重启完成！
echo ==============================
echo.
pause