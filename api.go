package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"
)

var (
	dirDatos string
	modoSel  string
	serv     *ladoServidor
	cliente  *ladoCliente
)

func randToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

// dirPrograma: carpeta al lado del exe (portable). Si no deja escribir,
// usa %LOCALAPPDATA%\VPNPrint y avisa en el log.
func dirPrograma() string {
	// Siempre usar AppData (escribible sin admin, persistente entre reinicios).
	// Antes se guardaba junto al exe, lo que fallaba en Program Files (UAC)
	// y causaba pérdida de config/certificado al reiniciar.
	base := filepath.Join(os.Getenv("LOCALAPPDATA"), "VPNPrint", "VPNPrintDatos")
	_ = os.MkdirAll(base, 0o755)

	// Migración: si había datos junto al exe, copiarlos a AppData.
	exe, _ := os.Executable()
	if exe != "" {
		viejo := filepath.Join(filepath.Dir(exe), "VPNPrintDatos")
		if viejo != base {
			for _, arch := range []string{"config.json", "servidor.cert", "servidor.key", "registro.txt"} {
				src := filepath.Join(viejo, arch)
				dst := filepath.Join(base, arch)
				if _, err := os.Stat(src); err == nil {
					if _, err2 := os.Stat(dst); err2 != nil {
						_ = copyFile(src, dst)
					}
				}
			}
			// migrar subcarpetas (cola, impresos)
			for _, sub := range []string{"cola", "impresos"} {
				srcDir := filepath.Join(viejo, sub)
				dstDir := filepath.Join(base, sub)
				if info, err := os.Stat(srcDir); err == nil && info.IsDir() {
					_ = os.MkdirAll(dstDir, 0o755)
					entradas, _ := os.ReadDir(srcDir)
					for _, e := range entradas {
						if !e.IsDir() {
							_ = copyFile(filepath.Join(srcDir, e.Name()), filepath.Join(dstDir, e.Name()))
						}
					}
				}
			}
		}
	}
	return base
}

func copyFile(src, dst string) error {
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, b, 0o600)
}

func probarEscritura(dir string) bool {
	_ = os.MkdirAll(dir, 0o755)
	p := filepath.Join(dir, "prueba.tmp")
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		return false
	}
	_ = os.Remove(p)
	return true
}

// principalIP descubre la IP local que conviene publicar en el código.
func principalIP() string {
	addrs, err := net.InterfaceAddrs()
	if err != nil {
		return ""
	}
	candi := []string{}
	for _, a := range addrs {
		if ipnet, ok := a.(*net.IPNet); ok && !ipnet.IP.IsLoopback() {
			if v4 := ipnet.IP.To4(); v4 != nil {
				candi = append(candi, v4.String())
			}
		}
	}
	sort.Strings(candi)
	// prioriza 192.168.x / 10.x
	for _, c := range candi {
		if strings.HasPrefix(c, "192.168.") || strings.HasPrefix(c, "10.") {
			return c
		}
	}
	if len(candi) > 0 {
		return candi[0]
	}
	return ""
}

// ------------------------- API para la GUI -------------------------

func jsonResp(w http.ResponseWriter, x any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_ = json.NewEncoder(w).Encode(x)
}

func leerJSON(r *http.Request, x any) error {
	return json.NewDecoder(r.Body).Decode(x)
}

func muxAPI() *http.ServeMux {
	m := http.NewServeMux()
	m.HandleFunc("/", paginaIndex)
	m.HandleFunc("/api/estado", apiEstado)
	m.HandleFunc("/api/elegir", apiElegir)
	m.HandleFunc("/api/config", apiConfig)
	m.HandleFunc("/api/codigo", apiCodigo)
	m.HandleFunc("/api/pegar-codigo", apiPegarCodigo)
	m.HandleFunc("/api/instalar", apiInstalar)
	m.HandleFunc("/api/quitar-impresora", apiQuitarImpresora)
	m.HandleFunc("/api/firewall", apiFirewall)
	m.HandleFunc("/api/impresoras", apiImpresoras)
	m.HandleFunc("/api/cola", apiCola)
	m.HandleFunc("/api/cola/accion", apiColaAccion)
	m.HandleFunc("/api/probar", apiProbar)
	m.HandleFunc("/api/registro", apiRegistro)
	m.HandleFunc("/api/arranque", apiArranque)
	return m
}

func empezarWeb(puerto int) int {
	for p := puerto; p < puerto+10; p++ {
		direccion := fmt.Sprintf("127.0.0.1:%d", p)
		if ln, err := net.Listen("tcp", direccion); err == nil {
			cfg.PuertoUI = p
			go func(ln net.Listener) {
				srv := &http.Server{Handler: muxAPI(), ReadHeaderTimeout: 10 * time.Second}
				_ = srv.Serve(ln)
			}(ln)
			return p
		}
	}
	return 0
}

func abrirBrowser(url string) {
	e := exec.Command("rundll32.exe", "url.dll,FileProtocolHandler", url)
	e.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	_ = e.Start()
}

type estadoUI struct {
	Modo      string `json:"modo"`
	Version   string `json:"version"`
	Datos     string `json:"datos"`
	WebPuerto int    `json:"webPuerto"`
	// servidor
	VerCodigo string   `json:"codigo,omitempty"`
	Impresora string   `json:"impresora,omitempty"`
	PuertoD   int      `json:"puertoD,omitempty"`
	IPs       []string `json:"ips,omitempty"`
	// cliente
	Servidor   string `json:"servidor,omitempty"`
	Conectado  string `json:"conectado,omitempty"`
	DriverSel  string `json:"driverSel,omitempty"`
	TipoVpn    string `json:"tipoVpn,omitempty"`
	DriverVpn  string `json:"driverVpn,omitempty"`
	IpFija     string `json:"ipFija,omitempty"`
	// cola + log
	Cola []tareaMeta `json:"cola"`
	Registros []string `json:"registro"`
	Impresoras []string `json:"impresorasListadas"`
	ImpresoraVpn struct {
		Presente bool   `json:"presente"`
		Detalle  string `json:"detalle,omitempty"`
	} `json:"impresoraVpn"`
	Arranque bool `json:"arranque"`
}

func apiEstado(w http.ResponseWriter, r *http.Request) {
	e := estadoUI{
		Modo: cfg.Modo, Version: version, Datos: dirDatos,
		WebPuerto: cfg.PuertoUI,
		Cola:      listaDeCola(),
	}
	if cfg.Modo == "servidor" {
		e.IPs = []string{principalIP()}
		if cfg.UrlServidor != "" { // base manual en modo servidor
			e.IPs = []string{cfg.UrlServidor}
		}
		e.PuertoD = cfg.PuertoDatos
		e.Impresora = cfg.ImpresoraDestino
		e.IpFija = cfg.UrlServidor
		e.VerCodigo = ""
		if cfg.Token != "" && (cfg.UrlServidor != "" || principalIP() != "") {
			base := cfg.UrlServidor
			if base == "" {
				base = principalIP()
			}
			e.VerCodigo = generarCodigo(base, cfg.PuertoDatos, cfg.Token, fingerprintServidor)
		}
	}
	if cfg.Modo == "cliente" {
		e.Servidor = cfg.UrlServidor
		if cfg.UrlServidor == "" {
			e.Conectado = "sin servidor: pega el código de conexión"
		} else if cliente != nil && len(cliente.listaCola()) > 0 {
			all := cliente.listaCola()
			pend := 0
			for _, m := range all {
				if m.Estado == estadoCola || m.Estado == estadoError {
					pend++
				}
			}
			if pend > 0 {
				e.Conectado = fmt.Sprintf("servidor configurado (%d en cola)", pend)
			} else {
				e.Conectado = "servidor configurado: todo al día"
			}
		} else if cfg.UrlServidor != "" {
			e.Conectado = "servidor configurado: todo al día"
		}
		e.DriverSel = elegidoDriver
		e.TipoVpn = tipoVirtual()
		e.DriverVpn = DriverImpresora(nomImpresora)
	}
	names, _ := EnumImpresoras()
	for _, n := range names {
		if n == nomImpresora {
			e.ImpresoraVpn.Presente = true
		}
	}
	e.Impresoras = names
	e.Arranque = arranqueActivo()
	e.Registros = glog.tail(200)
	jsonResp(w, e)
}

var elegidoDriver string

// recuperarPanic anota los panic de goroutines esenciales en el registro,
// en vez de dejar el hilo muerto en silencio.
func recuperarPanic(de string) {
	if r := recover(); r != nil {
		glog.add("PANICO en %s: %v", de, r)
	}
}


func listaDeCola() []tareaMeta {
	switch cfg.Modo {
	case "servidor":
		if serv != nil {
			return serv.listaCola()
		}
	case "cliente":
		if cliente != nil {
			return cliente.listaCola()
		}
	}
	return []tareaMeta{}
}

func apiElegir(w http.ResponseWriter, r *http.Request) {
	var p struct{ Modo string `json:"modo"` }
	if err := leerJSON(r, &p); err != nil || (p.Modo != "servidor" && p.Modo != "cliente") {
		http.Error(w, "modo invalido", http.StatusBadRequest)
		return
	}
	cfg.Modo = p.Modo
	if p.Modo == "servidor" {
		if cfg.Token == "" {
			cfg.Token = randToken()
		}
		if cfg.PuertoDatos == 0 {
			cfg.PuertoDatos = 8443
		}
		if cfg.ImpresoraDestino == "" {
			cfg.ImpresoraDestino = autodetectarImpresora()
		}
	} else {
		if cfg.PuertoUI == 0 {
			cfg.PuertoUI = 8422
		}
	}
	_ = saveConfig(dirDatos)
	arrancarModo()
	glog.add("modo elegido: %s", p.Modo)
	jsonResp(w, map[string]string{"ok": "1"})
}

func autodetectarImpresora() string {
	noms, err := EnumImpresoras()
	if err != nil {
		return ""
	}
	for _, n := range noms {
		ln := strings.ToLower(n)
		if strings.Contains(ln, "pdf") || strings.Contains(ln, "xps") || strings.Contains(ln, "onenote") ||
			strings.Contains(ln, "fax") || strings.Contains(ln, "virt") || strings.Contains(ln, "documentos") {
			continue
		}
		return n
	}
	if len(noms) > 0 {
		return noms[0]
	}
	return ""
}

func apiConfig(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodPost {
		var p Config
		if err := leerJSON(r, &p); err != nil {
			http.Error(w, "json invalido", http.StatusBadRequest)
			return
		}
		if cfg.Modo == "servidor" {
			if p.PuertoDatos > 0 && p.PuertoDatos < 65536 {
				cfg.PuertoDatos = p.PuertoDatos
			}
			nuevo := strings.TrimSpace(p.ImpresoraDestino)
			if nuevo != "" && esImpresoraVirtual(nuevo) {
				jsonResp(w, map[string]string{
					"ok":    "",
					"error": "«" + nuevo + "» es una impresora virtual (PDF/XPS): no acepta trabajos RAW y guarda archivos vacíos. Elegí la impresora física donde querés que salga el papel.",
				})
				return
			}
			cfg.ImpresoraDestino = nuevo
			if serv != nil {
				serv.setImpresora(cfg.ImpresoraDestino)
			}
			// base manual (opcional): IP o dominio del servidor
			cfg.UrlServidor = strings.TrimSpace(p.UrlServidor)
		}
		_ = saveConfig(dirDatos)
		glog.add("config actualizada")
	}
	jsonResp(w, cfg)
}

func apiCodigo(w http.ResponseWriter, r *http.Request) {
	if cfg.Modo != "servidor" || cfg.Token == "" {
		http.Error(w, "no sos servidor", http.StatusBadRequest)
		return
	}
	base := cfg.UrlServidor
	if base == "" {
		base = principalIP()
	}
	jsonResp(w, map[string]string{
		"codigo": generarCodigo(base, cfg.PuertoDatos, cfg.Token, fingerprintServidor),
		"ip":     principalIP(),
	})
}

func apiPegarCodigo(w http.ResponseWriter, r *http.Request) {
	var p struct{ Codigo string `json:"codigo"` }
	if err := leerJSON(r, &p); err != nil {
		http.Error(w, "json invalido", http.StatusBadRequest)
		return
	}
	if cfg.Modo != "cliente" {
		http.Error(w, "no sos cliente", http.StatusBadRequest)
		return
	}
	url, err := cliente.probarConexion(p.Codigo)
	if err != nil {
		jsonResp(w, map[string]string{"ok": "", "error": err.Error()})
		return
	}
	jsonResp(w, map[string]string{"ok": "1", "servidor": url})
}

func apiInstalar(w http.ResponseWriter, r *http.Request) {
	if cfg.Modo != "cliente" {
		http.Error(w, "instalar solo en cliente", http.StatusBadRequest)
		return
	}
	res, err := lanzarInstalacion(dirDatos)
	if err != nil {
		jsonResp(w, map[string]string{"ok": "", "error": err.Error()})
		return
	}
	if !res.OK {
		jsonResp(w, map[string]string{"ok": "", "error": res.Error, "paso": res.Paso, "driver": res.Driver})
		return
	}
	refrescarTipoVirtual()
	jsonResp(w, map[string]string{"ok": "1", "driver": res.Driver, "tipo": tipoVirtual()})
}

func apiFirewall(w http.ResponseWriter, r *http.Request) {
	if cfg.Modo != "servidor" {
		http.Error(w, "solo servidor", http.StatusBadRequest)
		return
	}
	e := exec.Command("powershell.exe", "-NoProfile", "-WindowStyle", "Hidden", "-Command",
		"Start-Process -FilePath '"+exeCotizado()+"' -ArgumentList '--firewall' -Verb RunAs -Wait")
	e.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := e.Start(); err != nil {
		jsonResp(w, map[string]string{"ok": "", "error": err.Error()})
		return
	}
	go e.Wait()
	jsonResp(w, map[string]string{"ok": "1"})
}

func apiQuitarImpresora(w http.ResponseWriter, r *http.Request) {
	e := exec.Command("powershell.exe", "-NoProfile", "-WindowStyle", "Hidden", "-Command",
		"Start-Process -FilePath '"+exeCotizado()+"' -ArgumentList '--quitar-impresora' -Verb RunAs -Wait")
	e.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	if err := e.Start(); err != nil {
		jsonResp(w, map[string]string{"ok": "", "error": err.Error()})
		return
	}
	go e.Wait()
	jsonResp(w, map[string]string{"ok": "1"})
}

func exeCotizado() string {
	exe, _ := os.Executable()
	out := make([]byte, 0, len(exe)*2)
	for i := 0; i < len(exe); i++ {
		out = append(out, '\'')
		out = append(out, exe[i])
	}
	_ = out
	return exe
}

func apiImpresoras(w http.ResponseWriter, r *http.Request) {
	noms, _ := EnumImpresoras()
	jsonResp(w, map[string]any{"impresoras": noms, "driver": elegidoDriver})
}

func apiCola(w http.ResponseWriter, r *http.Request) {
	jsonResp(w, listaDeCola())
}

func apiColaAccion(w http.ResponseWriter, r *http.Request) {
	var p struct{ Accion, ID string }
	if err := leerJSON(r, &p); err != nil {
		http.Error(w, "json invalido", http.StatusBadRequest)
		return
	}
	switch cfg.Modo {
	case "servidor":
		if serv != nil {
			serv.accionCola(p.Accion, p.ID)
		}
	case "cliente":
		if cliente != nil {
			cliente.accionCola(p.Accion, p.ID)
		}
	}
	jsonResp(w, map[string]string{"ok": "1"})
}

func apiProbar(w http.ResponseWriter, r *http.Request) {
	switch cfg.Modo {
	case "servidor":
		if serv == nil {
			jsonResp(w, map[string]string{"ok": "", "error": "El servidor de trabajos no está activo (el puerto de datos está ocupado por otra instancia). Cerrá las demás copias de VPNPrint."})
			return
		}
		if imp := serv.impresoraActual(); imp == "" {
			jsonResp(w, map[string]string{"ok": "", "error": "Primero elegí la impresora física y pulsá Guardar."})
			return
		} else if esImpresoraVirtual(imp) {
			jsonResp(w, map[string]string{"ok": "", "error": "«" + imp + "» es virtual (PDF/XPS): no imprime en papel. Elegí la impresora física."})
			return
		}
		serv.probaLocal()
	case "cliente":
		if cliente != nil {
			cliente.encoling("prueba VPNPrint", paginaPrueba(), true)
		}
	}
	jsonResp(w, map[string]string{"ok": "1"})
}

func apiRegistro(w http.ResponseWriter, r *http.Request) {
	jsonResp(w, map[string]any{"lineas": glog.tail(300)})
}

// autorun: botón para arrancar automáticamente al iniciar sesión (HKCU, sin admin)
func apiArranque(w http.ResponseWriter, r *http.Request) {
	var p struct{ On bool `json:"on"` }
	_ = leerJSON(r, &p)
	exe, _ := os.Executable()
	valor := "\"" + exe + "\" --bg"
	nombreReg := "VPNPrint"
	if strings.Contains(strings.ToLower(exe), "cliente") {
		nombreReg = "VPNPrint-Cliente"
	} else if strings.Contains(strings.ToLower(exe), "servidor") {
		nombreReg = "VPNPrint-Servidor"
	}
	if p.On {
		if err := regRunSet(nombreReg, valor, true); err != nil {
			jsonResp(w, map[string]string{"ok": "", "error": err.Error()})
			return
		}
		glog.add("arranque automatico activado")
	} else {
		if err := regRunSet(nombreReg, "", false); err != nil {
			jsonResp(w, map[string]string{"ok": "", "error": err.Error()})
			return
		}
		glog.add("arranque automatico quitado")
	}
	jsonResp(w, map[string]any{"ok": "1", "activo": arranqueActivo()})
}

// nombreTarea devuelve el nombre de la tarea programada para este exe.
func nombreTarea() string {
	exe, _ := os.Executable()
	if strings.Contains(strings.ToLower(exe), "cliente") {
		return "VPNPrint-Cliente"
	}
	return "VPNPrint-Servidor"
}

// regRunSet crea o elimina la entrada de autoarranque en el registro de Windows.
// Se usa la clave Run de HKCU (no requiere admin, más simple y fiable).
func regRunSet(nombre, valor string, poner bool) error {
	raiz := "HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Run"
	if poner {
		args := []string{"add", raiz, "/v", nombre, "/t", "REG_SZ", "/d", valor, "/f"}
		c := exec.Command("reg.exe", args...)
		c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
		out, err := c.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%v: %s", err, string(out))
		}
		return nil
	}
	c := exec.Command("reg.exe", "delete", raiz, "/v", nombre, "/f")
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	_, _ = c.CombinedOutput()
	return nil
}

func arranqueActivo() bool {
	exe, _ := os.Executable()
	nombreReg := "VPNPrint"
	if strings.Contains(strings.ToLower(exe), "cliente") {
		nombreReg = "VPNPrint-Cliente"
	} else if strings.Contains(strings.ToLower(exe), "servidor") {
		nombreReg = "VPNPrint-Servidor"
	}
	c := exec.Command("reg.exe", "query", "HKCU\\Software\\Microsoft\\Windows\\CurrentVersion\\Run", "/v", nombreReg)
	c.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	out, err := c.CombinedOutput()
	if err != nil {
		return false
	}
	return strings.Contains(string(out), "VPNPrint")
}
func escuchaAbierta(direccion string) bool {
	conn, err := net.DialTimeout("tcp", direccion, 2*time.Second)
	if err != nil { return false }
	_ = conn.Close()
	return true
}
