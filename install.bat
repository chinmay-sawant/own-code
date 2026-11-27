@echo off
set BIN=%USERPROFILE%\.local\bin
mkdir "%BIN%" 2>nul
set "STAGED=%BIN%\own-code-%RANDOM%-%RANDOM%.tmp"
set "SUMS=%TEMP%\own-code-SHA256SUMS-%RANDOM%-%RANDOM%.txt"
set "EXE="
set "RELEASE_URL="
if defined OWN_CODE_RELEASE_URL set "RELEASE_URL=%OWN_CODE_RELEASE_URL%"
if not defined RELEASE_URL set "RELEASE_URL=https://github.com/chinmay-sawant/own-code/releases/latest/download"
if /I "%PROCESSOR_ARCHITECTURE%"=="ARM64" set EXE=own-code-windows-arm64.exe
if /I "%PROCESSOR_ARCHITECTURE%"=="x86" echo 32-bit Windows releases are not published; build from source.
if /I "%PROCESSOR_ARCHITECTURE%"=="x86" exit /b 1
if not defined EXE set EXE=own-code-windows-amd64.exe
echo Detected %PROCESSOR_ARCHITECTURE%, installing %EXE%...
curl -fSL -o "%STAGED%" "%RELEASE_URL%/%EXE%"
if errorlevel 1 goto :failed
curl -fSL -o "%SUMS%" "%RELEASE_URL%/SHA256SUMS"
if errorlevel 1 goto :failed
set "GOT="
for /f %%H in ('certutil -hashfile "%STAGED%" SHA256 ^| findstr /I /R /X "[0-9a-f][0-9a-f]*"') do set "GOT=%%H"
if not defined GOT goto :badsum
findstr /I /C:"%GOT%  %EXE%" "%SUMS%" >nul
if errorlevel 1 goto :badsum
move /Y "%STAGED%" "%BIN%\own-code.exe" >nul
if errorlevel 1 goto :failed
del "%SUMS%" 2>nul
goto :pathset
:badsum
del "%STAGED%" 2>nul
del "%SUMS%" 2>nul
echo Checksum mismatch for %EXE%; the staged download was removed and the existing install was kept.
exit /b 1
:failed
del "%STAGED%" 2>nul
del "%SUMS%" 2>nul
echo Download or install failed. The existing install was kept. Retry, or download %EXE% by hand from https://github.com/chinmay-sawant/own-code/releases/latest and save it as %BIN%\own-code.exe
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
