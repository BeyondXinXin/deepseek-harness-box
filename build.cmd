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

rem Version: prefer DEEPSEEKHARNESSBOX_VERSION, then git describe, else dev.
if not defined DEEPSEEKHARNESSBOX_VERSION (
    for /f "delims=" %%v in ('git describe --tags --always --dirty 2^>nul') do set "DEEPSEEKHARNESSBOX_VERSION=%%v"
)
if not defined DEEPSEEKHARNESSBOX_VERSION set "DEEPSEEKHARNESSBOX_VERSION=dev"

rem Build fingerprint = DeepSeekHarnessBox version + DSH version: either
rem change triggers a payload rebuild. The DSH version is read from the global
rem package dir (npm root -g, different between local and CI) via
rem build-payload.ps1 -ProbeDshVersion. Keep probe commands OUT of if (...)
rem blocks (parens would close the block early), so a goto is used instead.
set "DSH_VERSION="
for /f "delims=" %%r in ('npm root -g 2^>nul') do set "NPM_ROOT=%%r"
if not defined NPM_ROOT goto dsh_version_done
set "DSH_VER_FILE=%TEMP%\deepseek-harness-box-dsh-version.txt"
powershell -NoProfile -ExecutionPolicy Bypass -File scripts\build-payload.ps1 -ProbeDshVersion -DshDir %NPM_ROOT%\@deepseek-ai\dsh > "%DSH_VER_FILE%"
if errorlevel 1 goto dsh_version_done
set /p DSH_VERSION=<"%DSH_VER_FILE%"
:dsh_version_done
rem Separator is "+" not "|": a pipe inside if (...) would be parsed as a
rem pipe even when quoted.
set "BUILD_KEY=%DEEPSEEKHARNESSBOX_VERSION%"
if defined DSH_VERSION set "BUILD_KEY=%BUILD_KEY%+%DSH_VERSION%"

rem Rebuild payload when "rebuild" is passed, payload.zip is missing, or the fingerprint changed.
set "NEED_PAYLOAD=0"
if /i "%~1"=="rebuild" set "NEED_PAYLOAD=1"
if not exist payload\payload.zip set "NEED_PAYLOAD=1"
if exist payload\.payload-version (
    set /p STORED=<payload\.payload-version
) else (
    set "STORED="
)
if not "%NEED_PAYLOAD%"=="1" if not "%STORED%"=="%BUILD_KEY%" set "NEED_PAYLOAD=1"

if "%NEED_PAYLOAD%"=="1" (
    echo Building payload.zip, fingerprint %BUILD_KEY% ...
    powershell -NoProfile -ExecutionPolicy Bypass -File scripts\build-payload.ps1 -Version "%DEEPSEEKHARNESSBOX_VERSION%"
    if errorlevel 1 exit /b 1
)

set "LDFLAGS=-H windowsgui -s -w -X github.com/BeyondXinXin/deepseek-harness-box/internal/version.Version=%DEEPSEEKHARNESSBOX_VERSION%"
"%GO_EXE%" build -trimpath -ldflags="%LDFLAGS%" -o DeepSeekHarnessBox.exe .\cmd\harnessbox
if errorlevel 1 exit /b 1

echo Built: %CD%\DeepSeekHarnessBox.exe
