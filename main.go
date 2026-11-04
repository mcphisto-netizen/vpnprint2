package main

import (
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"sync"

	"golang.org/x/sys/windows"
)

const version = "1.5"
const empezarPuertoUI = 8422

// mutex de instancia única
var onceMu sync.Mutex
var singleInstanceMutex windows.Handle

func main() {
	// ContinuarOnError: un argumento desconocido NO debe cerrar la app.
	flag.CommandLine.Init(os.Args[0], flag.ContinueOnError)
	flag.CommandLine.SetOutput(io.Discard)
	rutaSil := flag.Bool("instalar-silenciosa", false, "instala la impresora VPN (elevado)")
	rutaQui := flag.Bool("quitar-impresora", false, "quita la impresora VPN (elevado)")
	rutoFw := flag.Bool("firewall", false, "abre el puerto de datos en el firewall (elevado)")
	flag.Bool("bg", false, "arranca en segundo plano, sin abrir el navegador")
	tray := flag.Bool("tray", false, "ejecuta el monitor en la bandeja del sistema")
	_ = flag.CommandLine.Parse(os.Args[1:])

	// Single-instance mutex per exe/mode
	if !tryAcquireSingleInstance() {
		fmt.Println("Ya hay una instancia de VPNPrint ejecutándose para este modo.")
		os.Exit(1)
	}
	defer releaseSingleInstance()

	// Inicialización común
	dirDatos = dirPrograma()
	_ = os.MkdirAll(dirDatos, 0o755)
	if err := loadConfig(dirDatos); err != nil {
		glog.add("ERROR cargando config: %v", err)
	}
	initLog(dirDatos)

	// Auto-reparar HKCU Run si está activo
	if arranqueActivo() {
		if exe, err := os.Executable(); err == nil {
			nombreReg := "VPNPrint"
			if strings.Contains(strings.ToLower(exe), "cliente") {
				nombreReg = "VPNPrint-Cliente"
			} else if strings.Contains(strings.ToLower(exe), "servidor") {
				nombreReg = "VPNPrint-Servidor"
			}
			_ = regRunSet(nombreReg, "\""+exe+"\" --bg", true)
		}
	}

	if *rutaSil {
		instalarSilenciosa(dirDatos)
		return
	}
	if *rutaQui {
		quitarImpresoraElevado()
		return
	}
	if *tray {
		runTray()
		return
	}
	if *rutoFw {
		reglaFirewallElevado()
		return
	}

	// Detectar modo y arrancar motor
	args := os.Args[1:]
	guardado := cfg.Modo
	cfg.Modo = detectarModo(args)
	if guardado != "" && guardado != cfg.Modo {
		cfg.UrlServidor = ""
		glog.add("modo %s por el nombre del ejecutable (config.json decia %q); descarto los datos del modo anterior", cfg.Modo, guardado)
	}

	// pre-elegir driver
	elegidoDriver = elegirDriver()

	// cola de trabajos
	dirCola := dirDatos
	if cfg.Modo == "servidor" {
		dirCola = filepath.Join(dirDatos, "impresos")
	} else if cfg.Modo == "cliente" {
		dirCola = filepath.Join(dirDatos, "cola")
	}
	_ = os.MkdirAll(dirCola, 0o755)

	arrancarModo()

	webPuerto := empezarPuertoUI
	if cfg.PuertoUI > 0 {
		webPuerto = cfg.PuertoUI
	}
	pu := empezarWeb(webPuerto)
	if pu == 0 {
		glog.add("ERROR: no deje abrir el panel en puertos %d-%d (¿otro VPNPrint ya corre?)", webPuerto, webPuerto+9)
		return
	}
	cfg.PuertoUI = pu
	if err := saveConfig(dirDatos); err != nil {
		glog.add("ERROR guardando config: %v", err)
	}
	url := fmt.Sprintf("http://127.0.0.1:%d", pu)
	glog.add("panel local en %s (datos en %s)", url, dirDatos)

	fondo := enFondo()
	if fondo {
		go runTray()
	} else {
		abrirBrowser(url)
		go runTray()
	}

	// vivir para siempre
	select {}
}

func tryAcquireSingleInstance() bool {
	exe, _ := os.Executable()
	name := strings.ReplaceAll(filepath.Base(exe), ".exe", "")
	// Global namespace for mutex: "Global\\VPNPrint-<exe>"
	mutexName := "Global\\VPNPrint-" + name
	namePtr, err := windows.UTF16PtrFromString(mutexName)
	if err != nil {
		return true // si falla, permitir arrancar
	}
	h, err := windows.CreateMutex(nil, true, namePtr)
	if err != nil {
		return true
	}
	singleInstanceMutex = h
	// ERROR_ALREADY_EXISTS = 183
	if windows.GetLastError() == windows.ERROR_ALREADY_EXISTS {
		windows.CloseHandle(h)
		return false
	}
	return true
}

func releaseSingleInstance() {
	if singleInstanceMutex != 0 {
		windows.ReleaseMutex(singleInstanceMutex)
		windows.CloseHandle(singleInstanceMutex)
	}
}
func detectarModo(args []string) string {
	for _, a := range args {
		switch strings.ToLower(strings.TrimLeft(a, "-")) {
		case "servidor":
			return "servidor"
		case "cliente":
			return "cliente"
		}
	}
	// El nombre del ejecutable manda sobre lo guardado en config.json.
	// Si no, despues de probar el cliente una vez, VPNPrint-Servidor.exe
	// arrancaba en modo cliente y no dejaba configurar la impresora.
	exe := strings.ToLower(filepath.Base(os.Args[0]))
	if strings.Contains(exe, "servidor") {
		return "servidor"
	}
	if strings.Contains(exe, "cliente") {
		return "cliente"
	}
	if cfg.Modo == "servidor" || cfg.Modo == "cliente" {
		return cfg.Modo
	}
	return "" // lo elige el usuario en la GUI (ejecutable con nombre generico)
}

func enFondo() bool {
	for _, a := range os.Args[1:] {
		if strings.TrimSpace(a) == "--bg" {
			return true
		}
	}
	return false
}

func arrancarModo() {
	glog.add("ARRANQUE modo=%s datos=%s", cfg.Modo, dirDatos)
	switch cfg.Modo {
	case "servidor":
		if serv != nil {
			return
		}
		if cfg.PuertoDatos == 0 {
			cfg.PuertoDatos = 8443
		}
		if cfg.ImpresoraDestino == "" {
			cfg.ImpresoraDestino = autodetectarImpresora()
		}
		cert, _, err := asegurarCertificado(dirDatos)
		if err != nil {
			glog.add("ERROR certificado servidor: %v", err)
			return
		}
		domicilio := fmt.Sprintf("0.0.0.0:%d", cfg.PuertoDatos)
		ln, err := net.Listen("tcp", domicilio)
		if err != nil {
			glog.add("ERROR puerto de datos %d ocupado: %v", cfg.PuertoDatos, err)
			return
		}
		serv = nuevoLadoServidor(filepath.Join(dirDatos, "impresos"), cfg, func(c *Config) error {
			return saveConfig(dirDatos)
		})
		m := http.NewServeMux() // solo API de trabajos
		serv.muxRegistro(m)
		go func() {
			sTls := &http.Server{
				Handler:           m,
				ReadHeaderTimeout: 15 * time.Second,
			}
			tlsMas := &tls.Config{Certificates: []tls.Certificate{cert}}
			lt := tls.NewListener(ln, tlsMas)
			glog.add("servidor de trabajos activo en puerto %d", cfg.PuertoDatos)
			_ = sTls.Serve(lt)
		}()
		go serv.bucleImprimir()
	case "cliente":
		if cliente != nil {
			return
		}
		dirCola2 := filepath.Join(dirDatos, "cola")
		cliente = nuevoLadoCliente(dirCola2)
		go cliente.bucleEnviar()
		go cliente.bucleEscuchar()
		go cliente.buclePing()
	}
}

// reglaFirewallElevado abre el puerto de datos en el firewall de Windows.
func reglaFirewallElevado() {
	dirDatos = dirPrograma()
	_ = loadConfig(dirDatos)
	initLog(dirDatos)
	puerto := cfg.PuertoDatos
	if puerto == 0 {
		puerto = 8443
	}
	cmd := fmt.Sprintf(`netsh advfirewall firewall add rule name="VPNPrint Servidor" dir=in action=allow protocol=TCP localport=%d`, puerto)
	c := exec.Command("cmd.exe", "/c", cmd)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := c.CombinedOutput()
	if err != nil {
		glog.add("regla firewall fallo: %v %s", err, string(out))
	} else {
		glog.add("regla firewall agregada (puerto %d)", puerto)
	}
}

func quitarImpresoraElevado() {
	dirDatos = dirPrograma()
	_ = loadConfig(dirDatos)
	initLog(dirDatos)
	glog.add("quitando impresora VPN...")
	ps := fmt.Sprintf(
		"Remove-Printer -Name '%s' -ErrorAction SilentlyContinue; Remove-PrinterPort -Name '%s' -ErrorAction SilentlyContinue",
		nomImpresora, nomPuertoLocal,
	)
	c := exec.Command("powershell.exe", "-NoProfile", "-ExecutionPolicy", "Bypass", "-Command", ps)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	_, _ = c.CombinedOutput()
	// por si el puerto quedo en registro (windows viejo)
	base := `HKLM\SYSTEM\CurrentControlSet\Control\Print\Monitors\Standard TCP/IP Port\Ports\` + nomPuertoLocal
	r := exec.Command("reg.exe", "delete", base, "/f")
	r.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	_ = r.Run()
	glog.add("impresora VPN quitada")
}
