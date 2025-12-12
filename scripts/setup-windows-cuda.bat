@echo off
REM Setup yzma with CUDA support on Windows
REM Usage: setup-windows-cuda.bat [lib-path]

setlocal enabledelayedexpansion

echo.
echo ================================================
echo NornicDB Windows CUDA Setup
echo ================================================
echo.

REM Check if yzma is installed, if not install it
where yzma >nul 2>nul
if errorlevel 1 (
    echo Installing yzma CLI...
    echo.
    call go install github.com/hybridgroup/yzma/cmd/yzma@latest
    if errorlevel 1 (
        echo Error: Failed to install yzma
        echo Make sure Go is installed and in your PATH
        exit /b 1
    )
    echo yzma installed successfully
    echo.
) else (
    echo yzma CLI found
    echo.
)

REM Get lib path from argument or use default
if "%1"=="" (
    set "LIB_PATH=.\lib\llama"
) else (
    set "LIB_PATH=%1"
)

REM Create lib directory
if not exist "!LIB_PATH!" (
    mkdir "!LIB_PATH!"
)

echo Downloading CUDA-enabled llama.cpp libraries...
echo Target: !LIB_PATH!
echo.

REM Try to run yzma from PATH, if not found, use go run directly
where yzma >nul 2>nul
if errorlevel 1 (
    echo Using go run to invoke yzma...
    call go run github.com/hybridgroup/yzma/cmd/yzma@latest install --lib "!LIB_PATH!" --processor cuda
) else (
    yzma install --lib "!LIB_PATH!" --processor cuda
)

if errorlevel 1 (
    echo Error: Failed to download libraries
    exit /b 1
)

echo.
echo ================================================
echo Success! CUDA libraries installed
echo ================================================
echo.
echo Next steps:
echo.
echo 1. Set environment variable (PowerShell):
echo    $env:NORNICDB_LIB = '!LIB_PATH!'
echo.
echo 2. Or set permanently (run as Administrator):
echo    setx NORNICDB_LIB "!LIB_PATH!"
echo.
echo 3. Run NornicDB with GPU:
echo    .\nornicdb.exe
echo.
echo Verify GPU detection:
echo    .\nornicdb.exe --check-gpu
echo.
echo To update llama.cpp later:
echo    yzma install --lib "!LIB_PATH!" --processor cuda
echo.

endlocal
