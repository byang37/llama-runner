@echo off
setlocal
echo Building LLaMA Runner for Windows...
echo.
echo Requirements:
echo   - Go 1.21+  https://go.dev/dl/
echo   - TDM-GCC or MSYS2 mingw64 (for CGO)
echo   - WebView2 Runtime (built-in on Windows 11)
echo.

set CGO_ENABLED=1
set GOOS=windows
set GOARCH=amd64

go mod tidy
if errorlevel 1 goto :fail_tidy

rem Embed icon.ico via windres (ships with TDM-GCC / MinGW)
rem windres compiles app.rc into resource.syso which Go links automatically.
where windres >nul 2>&1
if errorlevel 1 goto :no_windres

echo Compiling resources with windres...
windres -i app.rc -o resource.syso -O coff -F pe-x86-64
if errorlevel 1 (
    echo WARN: windres failed, building without embedded icon
    del resource.syso 2>nul
)
goto :build

:no_windres
echo WARN: windres not found in PATH, building without embedded icon
echo       Install TDM-GCC: https://jmeubank.github.io/tdm-gcc/
del resource.syso 2>nul

:build
go build -ldflags="-H windowsgui -s -w" -o llama-runner.exe .
if errorlevel 1 goto :fail_build
del resource.syso 2>nul

echo.
echo Build complete: llama-runner.exe
echo Place llama-server.exe and its DLLs in the lib folder.
echo.
pause
exit /b 0

:fail_tidy
echo ERROR: go mod tidy failed
pause
exit /b 1

:fail_build
echo ERROR: Build failed
del resource.syso 2>nul
pause
exit /b 1
