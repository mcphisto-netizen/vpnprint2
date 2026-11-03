# VPNPrint

Imprimís en la impresora que está conectada a otra PC (por VPN u otra red),
sin carpetas compartidas, sin SMB y sin instalar drivers raros.

- En la PC con la impresora: **VPNPrint-Servidor.exe** (se instala una vez).
- En tu PC: **VPNPrint-Cliente.exe**. Aparece una impresora llamada
  **"Impresora VPN"**: imprimís normal desde cualquier programa y la hoja
  sale en la impresora de la otra PC.
- Si la red se corta, los trabajos quedan en cola y **se reintentan solos**.
- Todo se maneja por su **interfaz gráfica** (se abre sola en el navegador).
- Un solo ejecutable de ~5,5 MB, sin .NET ni dependencias: corre en
  Windows 7 en adelante (los `-32.exe` son para Windows de 32 bits).
- El registro queda en `VPNPrintDatos\registro.txt` junto al programa,
  y la GUI tiene botón "Copiar registro" para mandarlo y revisarlo.

## Uso

### PC con la impresora (servidor)
1. Ejecutar `VPNPrint-Servidor.exe`. El **nombre del archivo manda**: si dice
   `Servidor` arranca como servidor y si dice `Cliente` arranca como cliente,
   aunque `VPNPrintDatos\config.json` tenga guardado lo contrario (en ese caso
   descarta los datos del modo anterior y lo anota en el registro).
2. En la GUI: elegir la **impresora destino** (menú desplegable) y pulsar
   **"Abrir puerto en el firewall"** (pide permiso de administrador una vez).
3. Pulsar **"Copiar código"** del cuadro *Código de conexión* y mandarlo
   (WhatsApp/mail) a quien va a imprimir.

Solo puede haber **una** instancia de VPNPrint por carpeta. Si no sabés si
quedó una abierta, ejecutá `CERRAR-VPNPRINT.cmd` y volvé a abrir el ejecutable.

### PC que imprime (cliente)
1. Ejecutar `VPNPrint-Cliente.exe`.
2. Pegar el **código de conexión** y pulsar *Conectar*.
3. Pulsar **"Instalar impresora VPN"** (pide permiso de administrador una
   vez). Crea la impresora "Impresora VPN" con un driver que ya viene en
   Windows: **Microsoft XPS Document Writer** (v4 o v3) y, si no existe,
   "Generic / Text Only" (nada se descarga). Si la instalación falla, el
   registro (`VPNPrintDatos\registro.txt`) muestra el error exacto.
4. Imprimir normal en "Impresora VPN". Botón "Hoja de prueba" para verificar.
5. Opcional: tildar "Arrancar solo cuando prenda la PC" (queda listo en
   segundo plano sin abrir ventana).

## Cómo funciona por dentro

- El cliente crea una impresora con un **puerto TCP estándar apuntando a
  127.0.0.1:9100**: Windows le entrega los trabajos crudos a VPNPrint.
  Con el driver XPS lo que llega es un **paquete XPS** (el formato con el que
  Windows describe la página sin depender del driver de la impresora).
- VPNPrint los guarda en `VPNPrintDatos\cola\` y los manda por **HTTPS con
  token** (el certificado del servidor va fijado dentro del código, así que
  nadie puede inyectarse en el medio; no usa PKI del sistema).
- El servidor guarda el trabajo en `VPNPrintDatos\impresos\` (respuesta 202)
  y lo imprime **con el driver real de la impresora física**: Windows
  convierte el XPS al lenguaje de esa impresora y VPNPrint manda ese
  resultado al puerto. Así el papel sale igual que imprimiendo en el
  servidor, aunque el cliente tenga otro driver.
  Si la impresora falla, reintenta con espera creciente.
- La cola sobrevive cortes de red y reinicios: los archivos quedan en disco.

## Notas y límites conocidos

- **Modo abierto (sin autenticación)**: en esta versión la conexión entre cliente y
  servidor no requiere código ni token. Cualquier cliente que conozca la IP y el
  puerto del servidor puede conectarse. La validación de seguridad se reactivará
  en una etapa posterior.

- El código contiene IP + puerto + token: es el único "secreto". Si la IP
  del servidor cambia, cargala en "Dominio o IP fija" y actualizá el código.
  Conviene dejar la **IP fija** en la PC del servidor (o reservarla en el
  router por MAC) para que el código no deje de servir.
- Solo hay que abrir un puerto de entrada en el servidor (8443 por defecto,
  configurable). El cliente es saliente.
- La impresora destino debe ser **física**: las virtuales (Microsoft Print
  to PDF, PDF24, XPS, OneNote, fax) no aceptan trabajos RAW y generan
  archivos de 0 bytes. VPNPrint las rechaza al guardar y al probar.
- Si en el cliente no hay ningún driver XPS, VPNPrint instala la impresora
  con **"Generic / Text Only"**: imprime, pero **solo texto sin formato**
  (se pierden tipografías, imágenes y tamaños). La GUI avisa cuál quedó.
- Si el cliente tiene un driver de PDF (por ejemplo una instalación vieja),
  el servidor no puede reimprimir ese formato y lo marca como error con un
  mensaje claro: reinstalar la "Impresora VPN" lo cambia a XPS.
- El renderizado XPS usa `xpsprint.dll`, que viene en Windows 7 en adelante
  (en Windows "recortados" puede faltar; la GUI lo informa al imprimir).
- No corre en Windows XP (Go moderno lo abandonó). Vista/7/8/10/11 sí
  (compilado con Go 1.20; los `-32` cubren sistemas de 32 bits).
- Instalación de impresora y firewall piden UAC una vez: es lo único que
  necesita permisos; el resto funciona sin admin.

## Instalador

Hay un instalador estándar (Inno Setup) en `instalador\VPNPrint-Setup.exe`
que instala los ejecutables en `C:\Program Files\VPNPrint\` y crea accesos
directos en el menú Inicio:

- **VPNPrint - Ver estado** → abre http://127.0.0.1:8422 en el navegador
- **VPNPrint - Cerrar** → cierra todos los procesos de VPNPrint
- **VPNPrint - Desinstalar** → desinstalador estándar de Windows

Para regenerar el instalador después de compilar:
```
cd instalador
"C:\Program Files (x86)\Inno Setup 6\ISCC.exe" vpnprint.iss
```

## Compilar

Go 1.20+ (probado 1.20.14):

```
go build -trimpath -ldflags "-s -w -H=windowsgui" -o VPNPrint-Servidor.exe .
go build -trimpath -ldflags "-s -w -H=windowsgui" -o VPNPrint-Cliente.exe .
GOARCH=386 go build ... # versión 32 bits
```
=== IP / DHCP ===
Recomendación para evitar perdida de conexion tras reinicio:
- Fijar IP fija o reserva DHCP en el router (MAC/hostname).
- Repegar codigo tras cambiar IP del servidor.
- El cliente ahora reporta SIN CONEXION si el ping falla.
=== IP / DHCP ===
Recomendación para evitar perdida de conexion tras reinicio:
- Fijar IP fija o reserva DHCP en el router (MAC/hostname).
- Repegar codigo tras cambiar IP del servidor.
- El cliente ahora reporta SIN CONEXION si el ping falla.

