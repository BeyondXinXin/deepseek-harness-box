@echo off
setlocal

if not defined GO_EXE set "GO_EXE=go"
if exist "%GO_EXE%" goto go_found
where %GO_EXE% >nul 2>nul
if not errorlevel 1 goto go_found
echo Go not found. Install Go or set GO_EXE to the full path of go.exe.
exit /b 1

:go_found

if not exist assets\harnessbox.ico "%GO_EXE%" run .\cmd\makeicon
if errorlevel 1 exit /b 1

"%GO_EXE%" run github.com/akavel/rsrc@v0.10.2 -arch amd64 -manifest assets\harnessbox.exe.manifest -ico assets\harnessbox.ico -o cmd\harnessbox\rsrc_windows_amd64.syso
if errorlevel 1 exit /b 1

rem Version: prefer HARNESSBOX_VERSION, then git describe, else dev.
if not defined HARNESSBOX_VERSION (
    for /f "delims=" %%v in ('git describe --tags --always --dirty 2^>nul') do set "HARNESSBOX_VERSION=%%v"
)
if not defined HARNESSBOX_VERSION set "HARNESSBOX_VERSION=dev"

rem Rebuild payload when "rebuild" is passed, payload.zip is missing, or the version changed.
set "NEED_PAYLOAD=0"
if /i "%~1"=="rebuild" set "NEED_PAYLOAD=1"
if not exist payload\payload.zip set "NEED_PAYLOAD=1"
if exist payload\.payload-version (
    set /p STORED=<payload\.payload-version
) else (
    set "STORED="
)
if not "%NEED_PAYLOAD%"=="1" if not "%STORED%"=="%HARNESSBOX_VERSION%" set "NEED_PAYLOAD=1"

if "%NEED_PAYLOAD%"=="1" (
    echo Building payload.zip, version %HARNESSBOX_VERSION% ...
    powershell -NoProfile -ExecutionPolicy Bypass -File scripts\build-payload.ps1 -Version "%HARNESSBOX_VERSION%"
    if errorlevel 1 exit /b 1
    > payload\.payload-version echo %HARNESSBOX_VERSION%
)

set "LDFLAGS=-H windowsgui -s -w -X github.com/BeyondXinXin/harnessbox/internal/version.Version=%HARNESSBOX_VERSION%"
"%GO_EXE%" build -trimpath -ldflags="%LDFLAGS%" -o HarnessBox.exe .\cmd\harnessbox
if errorlevel 1 exit /b 1

echo Built: %CD%\HarnessBox.exe
