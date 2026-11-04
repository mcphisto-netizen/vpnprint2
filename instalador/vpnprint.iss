; VPNPrint Installer Script for Inno Setup 6
; Compilar con: "C:\Program Files (x86)\Inno Setup 6\ISCC.exe" vpnprint.iss

#define MyAppName "VPNPrint"
#define MyAppVersion "1.5"
#define MyAppPublisher "VPNPrint"
#define MyAppURL "https://github.com/"

[Setup]
AppName={#MyAppName}
AppVersion={#MyAppVersion}
AppPublisher={#MyAppPublisher}
AppPublisherURL={#MyAppURL}
DefaultDirName={autopf}\{#MyAppName}
DefaultGroupName={#MyAppName}
OutputDir=.
OutputBaseFilename=VPNPrint-Setup
Compression=lzma2
SolidCompression=yes
PrivilegesRequired=admin
ArchitecturesInstallIn64BitMode=x64
WizardStyle=modern
UninstallDisplayIcon={app}\VPNPrint-Servidor.exe

[Languages]
Name: "spanish"; MessagesFile: "compiler:Languages\Spanish.isl"

[Components]
Name: "servidor"; Description: "Servidor VPNPrint (PC con impresora)"; Types: full
Name: "cliente"; Description: "Cliente VPNPrint (PC que imprime)"; Types: full

[Files]
Source: "..\VPNPrint-Servidor.exe"; DestDir: "{app}"; Flags: ignoreversion; Components: servidor
Source: "..\VPNPrint-Cliente.exe"; DestDir: "{app}"; Flags: ignoreversion; Components: cliente
Source: "..\VPNPrint-Servidor-32.exe"; DestDir: "{app}"; Flags: ignoreversion; Components: servidor
Source: "..\VPNPrint-Cliente-32.exe"; DestDir: "{app}"; Flags: ignoreversion; Components: cliente
Source: "..\CERRAR-VPNPRINT.cmd"; DestDir: "{app}"; Flags: ignoreversion

[Icons]
Name: "{group}\VPNPrint - Servidor (iniciar)"; Filename: "{app}\VPNPrint-Servidor.exe"; IconFilename: "{app}\VPNPrint-Servidor.exe"
Name: "{group}\VPNPrint - Cliente (iniciar)"; Filename: "{app}\VPNPrint-Cliente.exe"; IconFilename: "{app}\VPNPrint-Cliente.exe"
Name: "{group}\VPNPrint - Ver estado"; Filename: "{app}\VPNPrint-AbrirEstado.cmd"; IconFilename: "{app}\VPNPrint-Servidor.exe"
Name: "{group}\VPNPrint - Cerrar"; Filename: "{app}\CERRAR-VPNPRINT.cmd"; IconFilename: "{app}\VPNPrint-Servidor.exe"
Name: "{group}\VPNPrint - Desinstalar"; Filename: "{uninstallexe}"

[Run]
Filename: "{app}\VPNPrint-Servidor.exe"; Description: "Abrir VPNPrint Servidor (configurar impresora)"; Flags: nowait postinstall skipifsilent unchecked
Filename: "{app}\VPNPrint-Cliente.exe"; Description: "Abrir VPNPrint Cliente (conectar al servidor)"; Flags: nowait postinstall skipifsilent unchecked

[Code]
procedure QuitarServicioViejo(const Nombre: String);
var
  rc: Integer;
begin
  // Versiones anteriores instalaban servicios LocalSystem. Hay que sacarlos
  // antes de copiar archivos: si quedan, arrancan al boot con otro directorio
  // de datos y se quedan con el puerto 9100.
  Exec('sc.exe', 'stop ' + Nombre, '', SW_HIDE, ewWaitUntilTerminated, rc);
  Exec('sc.exe', 'delete ' + Nombre, '', SW_HIDE, ewWaitUntilTerminated, rc);
end;

procedure CurStepChanged(CurStep: TSetupStep);
begin
  if CurStep = ssInstall then
  begin
    QuitarServicioViejo('VPNPrint-Servidor');
    QuitarServicioViejo('VPNPrint-Cliente');
    QuitarServicioViejo('VPNPrint');
  end;
  if CurStep = ssPostInstall then
  begin
    // Crear VPNPrint-AbrirEstado.cmd
    SaveStringToFile(ExpandConstant('{app}\VPNPrint-AbrirEstado.cmd'),
      '@echo off' + #13#10 + 'start http://127.0.0.1:8422' + #13#10, False);
  end;
end;

function NextButtonClick(CurPageID: Integer): Boolean;
var
  resultCode: Integer;
begin
  // Migración: borrar tareas programadas de versiones previas
  if CurPageID = wpReady then
  begin
    Exec('schtasks.exe', '/delete /tn VPNPrint-Servidor /f', '', SW_HIDE, True, resultCode);
    Exec('schtasks.exe', '/delete /tn VPNPrint-Cliente /f', '', SW_HIDE, True, resultCode);
    Exec('schtasks.exe', '/delete /tn VPNPrint /f', '', SW_HIDE, True, resultCode);
  end;
  Result := True;
end;