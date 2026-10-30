@echo off
setlocal
set BIN=%USERPROFILE%\.local\bin
mkdir "%BIN%" 2>nul
curl -fSL -o "%BIN%\own-code.exe" https://github.com/chinmay-sawant/own-code/releases/download/v0.1.0/own-code-windows-amd64.exe
if errorlevel 1 (
  echo Download failed. The v0.1.0 release asset may not exist yet.
  exit /b 1
)
echo Installed to %BIN%\own-code.exe
echo Add %BIN% to your PATH if needed.
