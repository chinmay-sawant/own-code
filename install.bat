@echo off
set BIN=%USERPROFILE%\.local\bin
mkdir "%BIN%" 2>nul
if /I "%PROCESSOR_ARCHITECTURE%"=="ARM64" set EXE=own-code-windows-arm64.exe
if /I "%PROCESSOR_ARCHITECTURE%"=="x86" echo 32-bit Windows releases are not published; build from source.
if /I "%PROCESSOR_ARCHITECTURE%"=="x86" exit /b 1
if not defined EXE set EXE=own-code-windows-amd64.exe
echo Detected %PROCESSOR_ARCHITECTURE%, installing %EXE%...
curl -fSL -o "%BIN%\own-code.exe" "https://github.com/chinmay-sawant/own-code/releases/latest/download/%EXE%"
if errorlevel 1 goto :failed
curl -fSL -o "%TEMP%\own-code-SHA256SUMS" "https://github.com/chinmay-sawant/own-code/releases/latest/download/SHA256SUMS"
if errorlevel 1 goto :failed
for /f %%H in ('certutil -hashfile "%BIN%\own-code.exe" SHA256 ^| findstr /R /X "[0-9a-f]*"') do set GOT=%%H
findstr /C:"%GOT%  %EXE%" "%TEMP%\own-code-SHA256SUMS" >nul
if errorlevel 1 goto :badsum
goto :pathset
:badsum
del "%BIN%\own-code.exe"
echo Checksum mismatch for %EXE%; the download was removed.
exit /b 1
:failed
echo Download failed. Retry, or download %EXE% by hand from https://github.com/chinmay-sawant/own-code/releases/latest and save it as %BIN%\own-code.exe
exit /b 1
:pathset
echo %PATH% | findstr /I /C:"%BIN%" >nul
if not errorlevel 1 echo %BIN% is already on PATH.
if not errorlevel 1 goto :usepath
setx PATH "%PATH%;%BIN%" >nul
echo Added %BIN% to your user PATH. New CMD windows can run 'own-code' from anywhere.
:usepath
set PATH=%BIN%;%PATH%
echo Installed to %BIN%\own-code.exe
echo Run it with: own-code
