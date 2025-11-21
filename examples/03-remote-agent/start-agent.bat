@echo off
REM Start the Boxy agent on Windows machine with Hyper-V

set BOXY=..\..\boxy.exe

echo === Starting Boxy Agent ===
echo.

REM Check if boxy exists
if not exist "%BOXY%" (
    echo Building boxy...
    cd ..\..
    go build -o boxy.exe ./cmd/boxy
    cd examples\03-remote-agent
    echo Build complete
    echo.
)

echo Configuration:
echo   Listen address: :50051
echo   TLS: disabled (insecure mode)
echo   Providers: auto-detect (Hyper-V on Windows)
echo.

echo WARNING: Running in insecure mode (no TLS)
echo   Credentials and data are sent unencrypted
echo   Only use on trusted networks (LAN/VPN)
echo.

echo The agent will:
echo   1. Detect available providers (Hyper-V on Windows)
echo   2. Start gRPC server on port 50051
echo   3. Wait for connections from Boxy server
echo.

echo Make sure:
echo   - Hyper-V is installed and running
echo   - Firewall allows incoming connections on port 50051
echo   - You have admin privileges (required for Hyper-V)
echo.

echo Press Ctrl+C to stop
echo.

%BOXY% agent serve --listen :50051 --config agent-config.yaml
