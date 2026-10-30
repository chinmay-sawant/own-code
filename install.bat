@echo off
set BIN=%USERPROFILE%\.local\bin
mkdir "%BIN%" 2>nul
if /I "%PROCESSOR_ARCHITECTURE%"=="ARM64" set EXE=own-code-windows-arm64.exe
if /I "%PROCESSOR_ARCHITECTURE%"=="x86" set EXE=own-code-windows-386.exe
if not defined EXE set EXE=own-code-windows-amd64.exe
echo Detected %PROCESSOR_ARCHITECTURE%, installing %EXE%...
curl -fSL -o "%BIN%\own-code.exe" "https://raw.githubusercontent.com/chinmay-sawant/own-code/master/windows-release/%EXE%"
if errorlevel 1 goto :failed
goto :pathset
:failed
echo Download failed. Retry, or download %EXE% by hand from the windows-release folder at https://github.com/chinmay-sawant/own-code/tree/master/windows-release and save it as %BIN%\own-code.exe
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
