@echo off
setlocal
title Cerrar VPNPrint
echo Cerrando cualquier VPNPrint que este abierto...
taskkill /F /IM VPNPrint-Servidor.exe    >nul 2>&1
taskkill /F /IM VPNPrint-Cliente.exe     >nul 2>&1
taskkill /F /IM VPNPrint-Servidor-32.exe >nul 2>&1
taskkill /F /IM VPNPrint-Cliente-32.exe  >nul 2>&1
taskkill /F /IM vpnprint.exe             >nul 2>&1
echo.
echo Listo. Ahora abri VPNPrint-Servidor.exe en la PC que tiene la impresora,
echo o VPNPrint-Cliente.exe en la PC que imprime.
echo.
pause
