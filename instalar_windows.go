//go:build windows

// Instalacion de la impresora virtual del cliente.
// Se hace elevando el propio exe con UAC (nada de archivos .bat):
//   - el proceso normal marca datos/pedido_instalar.json
//   - arranca una copia elevada de si mismo con --instalar-silenciosa
//   - esa copia elevada ejecuta PowerShell (puerto + impresora) con
//     reemplazo por registro si el cmdlet no existe en Windows viejo
//   - escribe datos/resultado_instalar.json y termina
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"time"
)

const (
	nomImpresora    = "Impresora VPN"
	nomPuertoLocal  = "VPNPRINT"
	puertoLocal9100 = 9100
)

type resultadoInstalar struct {
	OK      bool   `json:"ok"`
	Paso    string `json:"paso"`
	Driver  string `json:"driver"`
	Error   string `json:"error,omitempty"`
	Elevado bool   `json:"elevado"`
}

func archivoPedido(dir string) string  { return filepath.Join(dir, "pedido_instalar.json") }
func archivoResultado(dir string) string { return filepath.Join(dir, "resultado_instalar.json") }

// lanzarInstalacion eleva el install y espera el resultado. Llamado por la UI.
func lanzarInstalacion(dataDir string) (*resultadoInstalar, error) {
	driver := elegirDriver()
	if driver == "" {
		return nil, fmt.Errorf("este Windows no tiene un driver compatible. Activá «Microsoft XPS Document Writer» en Características de Windows y volvé a intentar.")
	}
	ped, _ := json.Marshal(resultadoInstalar{OK: false, Paso: "esperando", Driver: driver})
	if err := os.WriteFile(archivoResultado(dataDir), ped, 0o600); err != nil {
		return nil, err
	}
	p, _ := json.Marshal(resultadoInstalar{Paso: "pedido", Driver: driver})
	_ = os.WriteFile(archivoPedido(dataDir), p, 0o600)
	logaddInstalacion("pedidos elevados (driver=%s)", driver)
	exe, _ := os.Executable()
	exeFixed := fixLong(exe)
	ps := fmt.Sprintf(
		"Start-Process -FilePath '%s' -ArgumentList '--instalar-silenciosa' -Verb RunAs -Wait",
		stringsReplaceSingle(exeFixed),
	)
	cmd := exec.Command("powershell.exe", "-NoProfile", "-WindowStyle", "Hidden", "-Command", ps)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("no se puedo pedir permiso (UAC): %v", err)
	}
	go cmd.Wait()
	// esperar hasta 60 s el archivo de resultado
	for i := 0; i < 300; i++ {
		time.Sleep(200 * time.Millisecond)
		b, err := os.ReadFile(archivoResultado(dataDir))
		if err == nil {
			var r resultadoInstalar
			if json.Unmarshal(b, &r) == nil && r.Paso != "esperando" && r.Paso != "pedido" {
				if r.OK {
					glog.add("instalacion resultado: OK driver=%s", r.Driver)
				} else {
					glog.add("instalacion resultado: FALLO %s (%s)", r.Error, r.Paso)
				}
				return &r, nil
			}
		}
	}
	return nil, fmt.Errorf("la ventana de instalación no se completó (¿cancelaste el permiso?)")
}

func fixLong(s string) string { return s }

func stringsReplaceSingle(s string) string {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); i++ {
		if s[i] == '\'' {
			out = append(out, '\'', '\'')
		} else {
			out = append(out, s[i])
		}
	}
	return string(out)
}

func logaddInstalacion(f string, a ...any) { glog.add("instalar: "+f, a...) }

// instalarSilenciosa es el lado elevado.
func instalarSilenciosa(dir string) {
	run := func() (err error) {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("%v", r)
			}
		}()
		var ped resultadoInstalar
		if b, e := os.ReadFile(archivoPedido(dir)); e == nil {
			_ = json.Unmarshal(b, &ped)
		}
		driver := ped.Driver
		if driver == "" {
			driver = elegirDriver()
		}
		if driver == "" {
			return fmt.Errorf("no hay driver compatible encontrado")
		}
		logaddInstalacion("driver elegido: %s", driver)
		resultado := resultadoInstalar{Driver: driver, Elevado: true}
		defer func() {
			b, _ := json.Marshal(resultado)
			_ = os.WriteFile(archivoResultado(dir), b, 0o600)
		}()
		resultado.Paso = "creando puerto"
		if err := psCrePuerto(); err == nil {
			logaddInstalacion("puerto creado por cmdlet")
		} else {
			logaddInstalacion("cmdlet puerto fallo (%v), pruebo registro", err)
			if e2 := registroCrePuerto(); e2 != nil {
				return fmt.Errorf("no pude crear el puerto: %v / %v", err, e2)
			}
			logaddInstalacion("puerto creado por registro")
		}
		resultado.Paso = "creando impresora"
		if err := psCreImpresora(driver); err != nil {
			logaddInstalacion("cmdlet impresora fallo (%v), pruebo printui", err)
			if e2 := rundllCreImpresora(driver); e2 != nil {
				return fmt.Errorf("no pude crear la impresora %q: %v / %v", nomImpresora, err, e2)
			}
			logaddInstalacion("impresora creada por printui")
		} else {
			logaddInstalacion("impresora creada por cmdlet (driver=%s)", driver)
		}
		verifyScript := fmt.Sprintf("if (-not (Get-Printer -Name '%s' -ErrorAction SilentlyContinue)) { throw 'la impresora no quedo creada' }", nomImpresora)
		if err := psComando(verifyScript); err != nil {
			return fmt.Errorf("la impresora %q no quedo visible en Windows: %v", nomImpresora, err)
		}
		logaddInstalacion("impresora verificada en Windows")
		resultado.OK = true
		resultado.Paso = "listo"
		resultado.Driver = driver
		return nil
	}
	if err := run(); err != nil {
		glog.add("instalador elevado: error: %v", err)
		res := resultadoInstalar{Elevado: true, Paso: "error", Error: err.Error()}
		b, _ := json.Marshal(res)
		_ = os.WriteFile(archivoResultado(dir), b, 0o600)
	}
}

func psComando(script string) error {
	cmd := execCommandHidden("powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-Command", script)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v: %s", err, tailBytes(out, 300))
	}
	return nil
}

// execCommandHidden ejecuta sin que se vea ninguna ventana de consola.
func execCommandHidden(nombre string, args ...string) *exec.Cmd {
	c := exec.Command(nombre, args...)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	return c
}

func tailBytes(b []byte, n int) string {
	if len(b) > n {
		b = b[len(b)-n:]
	}
	return string(b)
}

func psCrePuerto() error {
	script := fmt.Sprintf(
		"$existing = Get-PrinterPort -Name '%s' -ErrorAction SilentlyContinue; "+
			"if ($existing) { exit 0 }; "+
			"$p = Add-PrinterPort -Name '%s' -PrinterHostAddress '127.0.0.1' -PortNumber %d -SNMP:$false -ErrorAction SilentlyContinue; "+
			"if (-not $p) { $p = Get-PrinterPort | Where-Object { $_.PrinterHostAddress -eq '127.0.0.1' -and $_.PortNumber -eq %d } | Select-Object -First 1 }; "+
			"if (-not $p) { throw 'no se pudo crear el puerto' }",
		nomPuertoLocal, nomPuertoLocal, puertoLocal9100, puertoLocal9100,
	)
	return psComando(script)
}

func psCreImpresora(driver string) error {
	// Si ya existe pero con OTRO driver (por ejemplo una version vieja con
	// "Microsoft Print To PDF"), hay que borrarla y crearla de nuevo: cambiar
	// solo el puerto deja el driver incompatible.
	// El puerto se busca dinámicamente porque el monitor TCP/IP puede crearlo
	// con un nombre distinto al solicitado ("IP_127.0.0.1", etc.).
	script := fmt.Sprintf(
		"$n='%s'; $d='%s'; "+
			"$port = Get-PrinterPort | Where-Object { $_.PrinterHostAddress -eq '127.0.0.1' -and $_.PortNumber -eq %d } | Select-Object -First 1; "+
			"if (-not $port) { "+
			"  $port = Add-PrinterPort -Name '%s' -PrinterHostAddress '127.0.0.1' -PortNumber %d -SNMP:$false -ErrorAction SilentlyContinue; "+
			"  if (-not $port) { $port = Get-PrinterPort | Where-Object { $_.PrinterHostAddress -eq '127.0.0.1' -and $_.PortNumber -eq %d } | Select-Object -First 1 } "+
			"}; "+
			"if (-not $port) { throw 'no se encontro el puerto TCP 127.0.0.1:9100' }; "+
			"$p = $port.Name; "+
			"$x=Get-Printer -Name $n -ErrorAction SilentlyContinue; "+
			"if ($x) { if ($x.DriverName -eq $d) { Set-Printer -Name $n -PortName $p | Out-Null; exit 0 } "+
			"else { Remove-Printer -Name $n -ErrorAction SilentlyContinue; Start-Sleep -Milliseconds 800 } }; "+
			"Add-Printer -Name $n -DriverName $d -PortName $p",
		nomImpresora, driver, puertoLocal9100, nomPuertoLocal, puertoLocal9100, puertoLocal9100,
	)
	return psComando(script)
}

// registroCrePuerto: para Windows viejos sin cmdlet Add-PrinterPort,
// escribe la llave del monitor TCP estándar y reinicia el spooler.
func registroCrePuerto() error {
	base := `HKLM\SYSTEM\CurrentControlSet\Control\Print\Monitors\Standard TCP/IP Port\Ports\` + nomPuertoLocal
	run := func(args ...string) error {
		c := exec.Command("reg.exe", args...)
		c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		if out, err := c.CombinedOutput(); err != nil {
			return fmt.Errorf("reg %v: %s", err, tailBytes(out, 200))
		}
		return nil
	}
	if err := run("add", base, "/v", "HostName", "/t", "REG_SZ", "/d", "127.0.0.1", "/f"); err != nil {
		return err
	}
	if err := run("add", base, "/v", "IPAddress", "/t", "REG_SZ", "/d", "127.0.0.1", "/f"); err != nil {
		return err
	}
	_ = run("add", base, "/v", "HWAddress", "/t", "REG_SZ", "/d", "", "/f")
	if err := run("add", base, "/v", "PortNumber", "/t", "REG_DWORD", "/d", fmt.Sprint(puertoLocal9100), "/f"); err != nil {
		return err
	}
	if err := run("add", base, "/v", "Protocol", "/t", "REG_DWORD", "/d", "1", "/f"); err != nil {
		return err
	}
	_ = run("add", base, "/v", "SNMP Community Name", "/t", "REG_SZ", "/d", "", "/f")
	_ = run("add", base, "/v", "SNMP Enabled", "/t", "REG_DWORD", "/d", "0", "/f")
	_ = run("add", base, "/v", "Queue", "/t", "REG_SZ", "/d", "", "/f")
	// reiniciar cola para que tome el puerto
	stop := exec.Command("cmd.exe", "/c", "net stop spooler & net start spooler")
	stop.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if out, err := stop.CombinedOutput(); err != nil {
		glog.add("spooler restart fallo: %v %s", err, tailBytes(out, 200))
	}
	return nil
}

// rundllCreImpresora: ultimo recurso en Windows muy viejo usando printui.
func rundllCreImpresora(driver string) error {
	inf := filepath.Join(os.Getenv("windir"), "inf", "ntprint.inf")
	script := fmt.Sprintf(
		`$p = Start-Process -FilePath 'rundll32.exe' -ArgumentList 'printui.dll,PrintUIEntry /if /b "%s" /f "%s" /r "%s" /m "%s" /z' -Verb RunAs -Wait -PassThru; exit $p.ExitCode`,
		nomImpresora, inf, nomPuertoLocal, driver,
	)
	cmd := exec.Command("powershell.exe", "-NoProfile", "-Command", script)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%v %s", err, tailBytes(out, 300))
	}
	return nil
}