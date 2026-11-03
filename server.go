package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ladoServidor gestiona recibir trabajos, guardarlos en cola e imprimirlos
// en la impresora fisica con reintentos.
type ladoServidor struct {
	dir      string
	puertoD  int
	impresor string

	mu      sync.Mutex
	trabajos map[string]*tareaMeta

	guardar func(*Config) error
}

type tareaMeta struct {
	ID        string `json:"id"`
	Nombre    string `json:"nombre"`
	Tamano    int64  `json:"tamano"`
	Intentos  int    `json:"intentos"`
	Proximo   int64  `json:"proximo"` // unix ms
	Error     string `json:"error,omitempty"`
	Detalle   string `json:"detalle,omitempty"`
	Creado    int64  `json:"creado"`
	Estado    string `json:"estado"` // "cola" | "hecho" | "error"
	GDI       bool   `json:"gdi,omitempty"`   // hoja de prueba: se dibuja con el driver real
	Tipo      string `json:"tipo,omitempty"`  // xps | pdf | texto | raw
}

// errorPermanente marca un fallo que no se arregla reintentando
// (formato que el servidor no sabe imprimir, driver inexistente, etc.).
type errorPermanente struct{ msg string }

func (e errorPermanente) Error() string { return e.msg }

// tipoTrabajo adivina el formato mirando los primeros bytes.
func tipoTrabajo(datos []byte) string {
	if len(datos) >= 4 && datos[0] == 'P' && datos[1] == 'K' && datos[2] == 3 && datos[3] == 4 {
		if bytes.Contains(datos, []byte(".fdseq")) || bytes.Contains(datos, []byte("FixedDocumentSequence")) {
			return "xps"
		}
		return "zip"
	}
	if bytes.HasPrefix(datos, []byte("%PDF")) {
		return "pdf"
	}
	return "raw"
}

const (
	estadoCola  = "cola"
	estadoHecho = "hecho"
	estadoError = "err"
)

func rutaMeta(dir, id string) string { return filepath.Join(dir, id+".meta.json") }
func rutaDato(dir, id string) string { return filepath.Join(dir, id+".dat") }

func nuevoLadoServidor(dir string, cfg *Config, guardar func(*Config) error) *ladoServidor {
	_ = os.MkdirAll(dir, 0o755)
	s := &ladoServidor{dir: dir, guardar: guardar, trabajos: map[string]*tareaMeta{}}
	s.puertoD = cfg.PuertoDatos
	if s.puertoD == 0 {
		s.puertoD = 8443
	}
	s.impresor = cfg.ImpresoraDestino
	s.cargarCola()
	return s
}

func (s *ladoServidor) cargarCola() {
	entradas, err := os.ReadDir(s.dir)
	if err != nil {
		return
	}
	for _, e := range entradas {
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(s.dir, e.Name()))
		if err != nil {
			continue
		}
		var m tareaMeta
		if json.Unmarshal(b, &m) == nil && m.ID != "" {
			s.trabajos[m.ID] = &m
		}
	}
	// borrar .dat sin meta de corridas rotas
	entradas2, _ := os.ReadDir(s.dir)
	for _, e := range entradas2 {
		if filepath.Ext(e.Name()) == ".dat" {
			id := e.Name()[:len(e.Name())-len(".dat")]
			if _, ok2 := s.trabajos[id]; !ok2 {
				_ = os.Remove(filepath.Join(s.dir, e.Name()))
			}
		}
	}
}

func (s *ladoServidor) autorizaToken(r *http.Request) bool {
	// Modo abierto (sin autenticacion): aceptar toda conexion.
	// TODO: reactivar validacion de token cuando el sistema este estable.
	return true
}

func (s *ladoServidor) muxRegistro(m *http.ServeMux) {
	m.HandleFunc("/v1/ping", s.ping)
	m.HandleFunc("/v1/trabajo", s.recibirTrabajo)
}

func (s *ladoServidor) ping(w http.ResponseWriter, r *http.Request) {
	if !s.autorizaToken(r) {
		http.Error(w, "token invalido", http.StatusUnauthorized)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"ok": "1", "app": "VPNPrint"})
}

func (s *ladoServidor) recibirTrabajo(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "solo POST", http.StatusMethodNotAllowed)
		return
	}
	if !s.autorizaToken(r) {
		glog.add("trabajo rechazado: token invalido desde %s", r.RemoteAddr)
		http.Error(w, "token invalido", http.StatusUnauthorized)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 512*1024*1024))
	if err != nil || len(body) == 0 {
		http.Error(w, "cuerpo vacio", http.StatusBadRequest)
		return
	}
	id := s.generarID()
	nom := "trabajo-" + time.Now().Format("150405")
	if n := r.Header.Get("X-VPNPrint-Nombre"); n != "" {
		nom = nomSanit(n)
	}
	m := &tareaMeta{ID: id, Nombre: nom, Tamano: int64(len(body)), Creado: time.Now().UnixMilli(), Estado: estadoCola, Proximo: 0}
	if r.Header.Get("X-VPNPrint-GDI") == "1" {
		m.GDI = true
	}
	// El tipo se deduce del CONTENIDO (es lo único confiable: si la
	// impresora virtual del cliente cambió de driver, la cabecera puede
	// mentir). La cabecera sólo sirve para distinguir texto plano de RAW.
	declarado := strings.ToLower(strings.TrimSpace(r.Header.Get("X-VPNPrint-Tipo")))
	switch detectado := tipoTrabajo(body); detectado {
	case "xps", "pdf", "zip":
		m.Tipo = detectado
	case "raw":
		if declarado == "texto" || declarado == "gdi" {
			m.Tipo = declarado
		} else {
			m.Tipo = "raw"
		}
	}
	if err := os.WriteFile(rutaDato(s.dir, id), body, 0o600); err != nil {
		http.Error(w, "no se pudo guardar", http.StatusInternalServerError)
		return
	}
	s.muXTarea()
	s.guardarMeta(m)
	s.liberaX()
	w.WriteHeader(http.StatusAccepted)
	_, _ = w.Write([]byte(`{"ok":"1"}`))
}

// esImpresoraVirtual detecta impresoras que NO aceptan trabajos RAW
// (Microsoft Print to PDF, PDF24, XPS, OneNote, fax, etc.): con RAW
// nunca generan el PDF y dejan archivos de 0 bytes.
func esImpresoraVirtual(nombre string) bool {
	n := strings.ToLower(nombre)
	for _, m := range []string{
		"print to pdf", "pdf24", "xps", "onenote", "fax", "cute pdf", "cutepdf",
		"dopdf", "pdfcreator", "adobe pdf", "nitro pdf", "bullzip", "primopdf",
		"microsoft print",
	} {
		if strings.Contains(n, m) {
			return true
		}
	}
	return false
}

func nomSanit(n string) string {
	r := []rune{}
	for _, c := range n {
		if c == '/' || c == '\\' || c == ':' || c == '*' || c == '?' || c == '"' || c == '<' || c == '>' || c == '|' {
			c = '-'
		}
		r = append(r, c)
	}
	if len(r) > 80 {
		r = r[:80]
	}
	return string(r)
}

func (s *ladoServidor) generarID() string { return fmt.Sprintf("J%s-%d", time.Now().Format("20060102-150405"), time.Now().UnixNano()%1_000_000) }

func (s *ladoServidor) muXTarea() { s.mu.Lock() }
func (s *ladoServidor) liberaX()  { s.mu.Unlock() }

// setImpresora actualiza la impresora destino en caliente
func (s *ladoServidor) setImpresora(nom string) {
	s.muXTarea()
	defer s.liberaX()
	s.impresor = nom
}

// impresoraActual devuelve la impresora destino configurada.
func (s *ladoServidor) impresoraActual() string {
	s.muXTarea()
	defer s.liberaX()
	return s.impresor
}

func (s *ladoServidor) guardarMeta(m *tareaMeta) {
	s.trabajos[m.ID] = m
	b, _ := json.Marshal(m)
	_ = os.WriteFile(rutaMeta(s.dir, m.ID), b, 0o600)
}

// bucleImprimir revisa la cola e imprime con reintentos y espera creciente.
func (s *ladoServidor) bucleImprimir() {
	defer recuperarPanic("bucleImprimir")
	for {
		time.Sleep(1 * time.Second)
		ahora := time.Now().UnixMilli()
		despertar := false
		s.muXTarea()
		ids := []string{}
		for id, m := range s.trabajos {
			if m.Estado == estadoCola && m.Proximo <= ahora {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		me := s.impresor
		s.liberaX()
		for _, id := range ids {
			despertar = true
			s.imprimirUno(id, me)
		}
		_ = despertar
	}
}

func (s *ladoServidor) imprimirUno(id, impresora string) {
	datos, err := os.ReadFile(rutaDato(s.dir, id))
	if err != nil {
		s.muXTarea()
		defer s.liberaX()
		if m, ok := s.trabajos[id]; ok {
			m.Estado = estadoError
			m.Error = "archivo de trabajo desaparecio"
			s.guardarMeta(m)
		}
		return
	}
	s.muXTarea()
	m, ok := s.trabajos[id]
	if !ok {
		s.liberaX()
		return
	}
	m.Intentos++
	nombre := m.Nombre
	tipo := m.Tipo
	if m.GDI {
		tipo = "gdi"
	}
	intentos := m.Intentos
	s.liberaX()

	// A partir de acá se imprime SIN el candado: la interfaz tiene que poder
	// consultar la cola aunque el driver tarde unos segundos.
	marcarError := func(msg string, definitivo bool) {
		s.muXTarea()
		defer s.liberaX()
		t, ok := s.trabajos[id]
		if !ok {
			return
		}
		t.Error = msg
		t.Detalle = ""
		if definitivo {
			t.Estado = estadoError
			t.Proximo = 0
		} else {
			backoff := time.Duration(intentos*15) * time.Second
			if backoff > 5*time.Minute {
				backoff = 5 * time.Minute
			}
			t.Proximo = time.Now().Add(backoff).UnixMilli()
		}
		s.guardarMeta(t)
	}
	if esImpresoraVirtual(impresora) {
		marcarError("«"+impresora+"» es una impresora virtual PDF: no acepta trabajos RAW. Elegí la impresora física en Configuración.", true)
		glog.add("trabajo %s: destino virtual rechazado (%s)", id, impresora)
		return
	}
	if listo, motivo := EstadoImpresora(impresora); !listo {
		marcarError("impresora no disponible: "+motivo+". Reintentando.", false)
		if intentos%5 == 1 {
			glog.add("trabajo %s espera: impresora %s %s", id, impresora, motivo)
		}
		return
	}
	if tipo == "" {
		tipo = tipoTrabajo(datos)
	}
	var detalle string
	var err2 error
	switch tipo {
	case "gdi", "texto":
		lineas := strings.Split(strings.ReplaceAll(string(datos), "\r\n", "\n"), "\n")
		err2 = ImprimirTextoGDI(impresora, nombre, lineas)
	case "xps":
		detalle, err2 = ImprimirXPS(impresora, nombre, datos)
	case "pdf":
		err2 = errorPermanente{"el cliente mandó un PDF y el servidor no puede imprimirlo. En el cliente, reinstalá la «Impresora VPN» para que use el driver XPS."}
	case "zip":
		err2 = errorPermanente{"el archivo recibido no es un XPS válido (paquete desconocido)."}
	default:
		detalle, err2 = ImprimirRAW(impresora, nombre, datos)
	}
	s.muXTarea()
	defer s.liberaX()
	m, ok = s.trabajos[id]
	if !ok {
		return
	}
	if err2 == nil {
		m.Estado = estadoHecho
		m.Error = ""
		if detalle != "" {
			m.Detalle = detalle
		} else {
			m.Detalle = "enviado a " + impresora
		}
		m.Proximo = 0
		_ = os.Remove(rutaDato(s.dir, id))
		glog.add("trabajo %s (%s, %s) -> %s: %s", id, m.Nombre, tipo, impresora, m.Detalle)
	} else if _, perm := err2.(errorPermanente); perm {
		m.Estado = estadoError
		m.Error = err2.Error()
		m.Detalle = ""
		m.Proximo = 0
		glog.add("trabajo %s (%s, %s) error definitivo: %v", id, m.Nombre, tipo, err2)
	} else {
		m.Error = err2.Error()
		m.Detalle = ""
		backoff := time.Duration(intentos*15) * time.Second
		if backoff > 5*time.Minute {
			backoff = 5 * time.Minute
		}
		m.Proximo = time.Now().Add(backoff).UnixMilli()
		if intentos%5 == 1 {
			glog.add("trabajo %s impresion fallo int.%d: %v", id, intentos, err2)
		}
	}
	s.guardarMeta(m)
}

func (s *ladoServidor) listaCola() []tareaMeta {
	s.muXTarea()
	defer s.liberaX()
	out := []tareaMeta{}
	for _, m := range s.trabajos {
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Creado > out[j].Creado })
	return out
}

func (s *ladoServidor) accionCola(accion, id string) {
	s.muXTarea()
	defer s.liberaX()
	ids := []string{}
	if id != "" {
		ids = append(ids, id)
	} else {
		for k := range s.trabajos {
			ids = append(ids, k)
		}
	}
	for _, k := range ids {
		m, ok := s.trabajos[k]
		if !ok {
			continue
		}
		switch accion {
		case "reintentar":
			if id == "" && m.Estado == estadoHecho {
				continue
			}
			m.Estado = estadoCola
			m.Proximo = 0
			m.Intentos = 0
			s.guardarMeta(m)
		case "borrar":
			_ = os.Remove(rutaDato(s.dir, k))
			_ = os.Remove(rutaMeta(s.dir, k))
			delete(s.trabajos, k)
		}
	}
}

// probaLocal genera y encola una página de prueba local. Se imprime por GDI
// (con el driver real), porque el texto crudo RAW lo ignora la mayoría de
// impresoras.
func (s *ladoServidor) probaLocal() {
	s.muXTarea()
	defer s.liberaX()
	id := s.generarID()
	txt := paginaPrueba()
	m := &tareaMeta{ID: id, Nombre: "Prueba VPNPrint", Tamano: int64(len(txt)), Creado: time.Now().UnixMilli(), Estado: estadoCola, GDI: true}
	_ = os.WriteFile(rutaDato(s.dir, id), txt, 0o600)
	s.guardarMeta(m)
}

// paginaPrueba crea los bytes de una hoja de prueba en texto plano.
// El servidor la dibuja con GDI; el cliente la manda y el servidor la dibuja.
func paginaPrueba() []byte {
	return []byte("VPNPrint - pagina de prueba\r\n" +
		"Si lees esto, la impresora funciona.\r\n" +
		"Fecha: " + time.Now().Format("2006-01-02 15:04:05") + "\r\n")
}
