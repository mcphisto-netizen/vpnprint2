# Plan: Instalador estándar para VPNPrint

## Objetivo
Crear un instalador de Windows (.exe) para VPNPrint que:
- Instale el ejecutable y los datos en `C:\Program Files\VPNPrint\` (o carpeta elegida por el usuario)
- Cree accesos directos en el menú Inicio
- Permita ver el estado de la impresora en el navegador
- Permita cerrar VPNPrint fácilmente
- Incluya desinstalador estándar

## Herramienta recomendada: Inno Setup 6
- Gratuito, ampliamente usado, genera instaladores `.exe` pequeños
- Soporta instalar por modo (servidor/cliente) según el nombre del archivo
- Crea accesos directos, desinstalador, y puede ejecutar comandos elevados
- URL: https://jrsoftware.org/isdl.php

## Estructura del instalador

```
VPNPrint-Setup.exe
├── Instala en: C:\Program Files\VPNPrint\
│   ├── VPNPrint-Servidor.exe
│   ├── VPNPrint-Cliente.exe
│   ├── VPNPrint-Servidor-32.exe
│   ├── VPNPrint-Cliente-32.exe
│   └── VPNPrintDatos\  (se crea en primer arranque, no se incluye)
├── Menú Inicio → VPNPrint\
│   ├── "VPNPrint - Estado (servidor)"  → abre http://127.0.0.1:8422
│   ├── "VPNPrint - Estado (cliente)"   → abre http://127.0.0.1:8422
│   ├── "VPNPrint - Cerrar"             → ejecuta taskkill
│   └── "VPNPrint - Desinstalar"        → desinstalador de Inno
└── (Opcional) Acceso directo en Escritorio
```

## Accesos directos a crear

### 1. "VPNPrint - Ver estado"
- Tipo: Acceso directo a URL (`.url`) o `.bat` que abre el navegador
- Comando: `cmd /c start http://127.0.0.1:8422`
- Nota: el puerto puede variar (8422-8430). El programa ya auto-incrementa si está ocupado. Para el acceso directo, usar el puerto por defecto 8422.

### 2. "VPNPrint - Cerrar"
- Tipo: Acceso directo a `.bat` o `.cmd`
- Comando: `taskkill /F /IM VPNPrint-Servidor.exe /IM VPNPrint-Cliente.exe /IM VPNPrint-Servidor-32.exe /IM VPNPrint-Cliente-32.exe`
- Reutilizar el existente `CERRAR-VPNPRINT.cmd`

### 3. "VPNPrint - Desinstalar"
- Inno Setup genera esto automáticamente

## Pasos de implementación

### Paso 1: Descargar e instalar Inno Setup 6
- Descargar de https://jrsoftware.org/isdl.php
- Instalar en `C:\Program Files (x86)\Inno Setup 6\`

### Paso 2: Crear el script `vpnprint.iss`
```iss
; VPNPrint Installer
#define MyAppName "VPNPrint"
#define MyAppVersion "1.5"
#define MyAppPublisher "VPNPrint"
#define MyAppURL "https://github.com/..."

[Setup]
AppName={#MyAppName}
AppVersion={#MyAppVersion}
DefaultDirName={autopf}\{#MyAppName}
DefaultGroupName={#MyAppName}
OutputDir=.\instalador
OutputBaseFilename=VPNPrint-Setup
Compression=lzma2
SolidCompression=yes
PrivilegesRequired=admin
ArchitecturesInstallIn64BitMode=x64

[Files]
Source: "VPNPrint-Servidor.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "VPNPrint-Cliente.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "VPNPrint-Servidor-32.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "VPNPrint-Cliente-32.exe"; DestDir: "{app}"; Flags: ignoreversion
Source: "CERRAR-VPNPRINT.cmd"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{group}\VPNPrint - Ver estado"; Filename: "{app}\VPNPrint-AbrirEstado.cmd"
Name: "{group}\VPNPrint - Cerrar"; Filename: "{app}\CERRAR-VPNPRINT.cmd"
Name: "{group}\VPNPrint - Desinstalar"; Filename: "{uninstallexe}"

[Run]
Filename: "{app}\VPNPrint-AbrirEstado.cmd"; Description: "Abrir estado de VPNPrint"; Flags: nowait postinstall skipifsilent

[Code]
// Preguntar modo (servidor o cliente) y renombrar el exe principal
```

### Paso 3: Crear `VPNPrint-AbrirEstado.cmd`
```bat
@echo off
start http://127.0.0.1:8422
```

### Paso 4: Compilar
```bat
"C:\Program Files (x86)\Inno Setup 6\ISCC.exe" vpnprint.iss
```

### Paso 5: Probar el instalador
- Ejecutar en una VM o máquina de prueba
- Verificar que los accesos directos aparecen en el menú Inicio
- Verificar que "Ver estado" abre el navegador
- Verificar que "Cerrar" cierra VPNPrint
- Verificar que el desinstalador limpia todo

## Alternativa sin instalador externo (Go nativo)

Si no se quiere depender de Inno Setup, se puede agregar un flag `--instalar-menu` al propio exe que:
- Crea los accesos directos en `%APPDATA%\Microsoft\Windows\Start Menu\Programs\VPNPrint\`
- Usa el API de Windows (IShellLink) para crear .lnk
- Un flag `--desinstalar-menu` los elimina

Esto es más simple pero no da desinstalador estándar ni instalación en Program Files.

## Recomendación

Usar **Inno Setup** para una experiencia profesional. El script `.iss` se puede versionar en el repo junto al código.

## Archivos a crear
- `instalador/vpnprint.iss` — script de Inno Setup
- `instalador/VPNPrint-AbrirEstado.cmd` — acceso directo para ver estado
- Actualizar `README.md` con instrucciones de compilación del instalador
- Agregar `instalador/` al `.gitignore` (o no, para versionarlo)

## Notas
- El instalador debe incluir ambos exes (servidor y cliente) o preguntar al usuario cuál instalar
- Para sistemas de 32 bits, incluir los `-32.exe`
- El instalador debe crear la carpeta `VPNPrintDatos` con permisos de escritura
- El autoarranque (HKCU Run) se puede configurar durante la instalación o dejar que el usuario lo active desde la GUI
