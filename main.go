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

	"golang.org/x/sys/windows/svc"
)

const version = "1.5"
const empezarPuertoUI = 8422

var fingerprintServidor string

func main() {
	// ContinuarOnError: un argumento desconocido NO debe cerrar la app.
	// (el arranque automatico usa "--bg" y antes moria con "flag provided but not defined")
	flag.CommandLine.Init(os.Args[0], flag.ContinueOnError)
	flag.CommandLine.SetOutput(io.Discard)
	rutaSil := flag.Bool("instalar-silenciosa", false, "instala la impresora VPN (elevado)")
	rutaQui := flag.Bool("quitar-impresora", false, "quita la impresora VPN (elevado)")
	rutoFw := flag.Bool("firewall", false, "abre el puerto de datos en el firewall (elevado)")
	flag.Bool("bg", false, "arranca en segundo plano, sin abrir el navegador")
	tray := flag.Bool("tray", false, "ejecuta el monitor en la bandeja del sistema")
	servicio := flag.Bool("servicio", false, "ejecuta como servicio de Windows")
	instalarServ := flag.Bool("instalar-servicio", false, "instala el servicio de Windows")
	desinstalarServ := flag.Bool("desinstalar-servicio", false, "desinstala el servicio de Windows")
	_ = flag.CommandLine.Parse(os.Args[1:])

	if *instalarServ {
		if err := instalarServicio(); err != nil {
			fmt.Println("ERROR:", err)
			os.Exit(1)
		}
		fmt.Println("Servicio instalado correctamente")
		return
	}
	if *desinstalarServ {
		if err := desinstalarServicio(); err != nil {
			fmt.Println("ERROR:", err)
			os.Exit(1)
		}
		fmt.Println("Servicio desinstalado correctamente")
		return
	}
	if *servicio {
		dirDatos = dirPrograma()
		_ = os.MkdirAll(dirDatos, 0o755)
		_ = loadConfig(dirDatos)
		initLog(dirDatos)
		arrancarModo()
		exe, _ := os.Executable()
		nombre := "VPNPrint"
		if strings.Contains(strings.ToLower(exe), "cliente") {
			nombre = "VPNPrint-Cliente"
		} else if strings.Contains(strings.ToLower(exe), "servidor") {
			nombre = "VPNPrint-Servidor"
		}
		err := svc.Run(nombre, &servicioVPNPrint{nombre: nombre})
		if err != nil {
			glog.add("ERROR servicio: %v", err)
		}
		return
	}

	dirDatos = dirPrograma()
	_ = os.MkdirAll(dirDatos, 0o755)
	_ = loadConfig(dirDatos)
	initLog(dirDatos)
	// Autorreparar la ruta del arranque automatico si el exe se movio.
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

	args := os.Args[1:]
	guardado := cfg.Modo
	cfg.Modo = detectarModo(args)
	if guardado != "" && guardado != cfg.Modo {
		// Se paso de un modo al otro: lo guardado del modo anterior estorba
		// (por ej. un token de otro servidor hacia que el codigo nuevo no sirviera).
		cfg.UrlServidor = ""
		cfg.Fingerprint = ""
		cfg.Token = ""
		glog.add("modo %s por el nombre del ejecutable (config.json decia %q); descarto los datos del modo anterior", cfg.Modo, guardado)
	}

	// pre-elegir driver (para mostrar en la UI aunque no instale)
	elegidoDriver = elegirDriver()

	// cola de trabajos (cliente impuestos y servidor: impresos)
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
	_ = saveConfig(dirDatos)
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
		if cfg.Token == "" {
			cfg.Token = randToken()
			_ = saveConfig(dirDatos)
		}
		if cfg.PuertoDatos == 0 {
			cfg.PuertoDatos = 8443
		}
		if cfg.ImpresoraDestino == "" {
			cfg.ImpresoraDestino = autodetectarImpresora()
		}
		cert, fp, err := asegurarCertificado(dirDatos)
		if err != nil {
			glog.add("ERROR certificado servidor: %v", err)
			return
		}
		fingerprintServidor = fp
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
