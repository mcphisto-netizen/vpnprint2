package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"
)

// ladoCliente: acepta lo que la impresora virtual deposita en el puerto
// local 9100, lo guarda en cola y lo manda al servidor con reintentos.
type ladoCliente struct {
	dir string

	tc *http.Client
	mu sync.Mutex
	tar  map[string]*tareaMeta
}

func nuevoLadoCliente(dir string) *ladoCliente {
	_ = os.MkdirAll(dir, 0o755)
		c := &ladoCliente{dir: dir, tar: map[string]*tareaMeta{}}
	c.rehacerCliente()
	c.cargarCola()
	return c
}

// tipoVirtual dice qué formato genera la impresora virtual instalada:
// xps (ideal: el servidor lo imprime con el driver real), pdf (el servidor
// no puede), texto (se dibuja con GDI, sin formato) o raw (driver propio).
var (
	tipoVirtualCache string
	tipoVirtualListo bool
)

func tipoVirtual() string {
	if !tipoVirtualListo {
		refrescarTipoVirtual()
	}
	return tipoVirtualCache
}

func refrescarTipoVirtual() {
	drv := strings.ToLower(DriverImpresora(nomImpresora))
	t := ""
	switch {
	case drv == "":
		t = ""
	case strings.Contains(drv, "xps"):
		t = "xps"
	case strings.Contains(drv, "pdf"):
		t = "pdf"
	case strings.Contains(drv, "text"):
		t = "texto"
	default:
		t = "raw"
	}
	tipoVirtualCache = t
	tipoVirtualListo = true
}

func (c *ladoCliente) rehacerCliente() {
	tr := &http.Transport{}
	if cfg.Fingerprint != "" || cfg.UrlServidor != "" {
		tr.TLSClientConfig = tlsCliente(cfg.Fingerprint)
	}
	c.tc = &http.Client{Transport: tr, Timeout: 120 * time.Second}
}

func (c *ladoCliente) cargarCola() {
	entradas, err := os.ReadDir(c.dir)
	if err != nil {
		return
	}
	for _, e := range entradas {
		if filepath.Ext(e.Name()) != ".json" {
			continue
		}
		b, err := os.ReadFile(filepath.Join(c.dir, e.Name()))
		if err != nil {
			continue
		}
		var m tareaMeta
		if json.Unmarshal(b, &m) == nil && m.ID != "" {
			if m.Estado != estadoHecho {
				m.Estado = estadoCola
			}
			c.tar[m.ID] = &m
		}
	}
}

// bucleEnviar manda la cola al servidor con esos reintentos eternos
// (si no hay red vuelve a intentar para siempre mientras el exe corre).
func (c *ladoCliente) bucleEnviar() {
	defer recuperarPanic("bucleEnviar")
	for {
		time.Sleep(2 * time.Second)
		if cfg.UrlServidor == "" {
			continue
		}
		ahora := time.Now().UnixMilli()
		c.mu.Lock()
		ids := []string{}
		for id, m := range c.tar {
			if m.Estado == estadoCola && m.Proximo <= ahora {
				ids = append(ids, id)
			}
		}
		sort.Strings(ids)
		c.mu.Unlock()
		for _, id := range ids {
			c.enviarUno(id)
		}
	}
}

func (c *ladoCliente) enviarUno(id string) {
	datos, err := os.ReadFile(rutaDato(c.dir, id))
	if err != nil {
		c.mu.Lock()
		if m, ok := c.tar[id]; ok {
			m.Estado = estadoError
			m.Error = "archivo de trabajo desaparecio"
			c.guardarMeta(m)
		}
		c.mu.Unlock()
		return
	}
	if len(datos) == 0 {
		c.mu.Lock()
		delete(c.tar, id)
		c.mu.Unlock()
		_ = os.Remove(rutaMeta(c.dir, id))
		_ = os.Remove(rutaDato(c.dir, id))
		return
	}
	nombre2 := ""
	c.mu.Lock()
	m, ok := c.tar[id]
	esGDI := false
	if ok {
		nombre2 = m.Nombre
		esGDI = m.GDI
		m.Intentos++
	}
	c.mu.Unlock()
	if !ok {
		return
	}
	url := cfg.UrlServidor + "/v1/trabajo"
	req, err := http.NewRequest("POST", url, bytes.NewReader(datos))
	if err != nil {
		c.marcarError(id, "url del servidor invalida", 60*time.Second)
		return
	}
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("X-VPNPrint-Token", cfg.Token)
	req.Header.Set("X-VPNPrint-Nombre", nombre2)
	if esGDI {
		req.Header.Set("X-VPNPrint-GDI", "1")
	}
	if tipo := tipoVirtual(); tipo != "" {
		req.Header.Set("X-VPNPrint-Tipo", tipo)
	}
	resp, err := c.tc.Do(req)
	if err != nil {
		c.reintenta(id, fmt.Errorf("no hay conexion con el servidor: %v", err))
		return
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4096))
	if resp.StatusCode == http.StatusUnauthorized {
		c.marcarError(id, "el servidor rechazo el codigo (token invalido)", time.Hour)
		glog.add("trabajo %s rechazado por token invalido", id)
		return
	}
	if resp.StatusCode != http.StatusAccepted {
		c.reintenta(id, fmt.Errorf("el servidor respondio %s", resp.Status))
		return
	}
	// ACK del servidor: borrar de la cola (servidor ya lo tiene guardado)
	c.mu.Lock()
	delete(c.tar, id)
	c.mu.Unlock()
	_ = os.Remove(rutaMeta(c.dir, id))
	_ = os.Remove(rutaDato(c.dir, id))
	glog.add("trabajo %s (%s, %d bytes) enviado al servidor", id, nombre2, len(datos))
}

func (c *ladoCliente) reintenta(id string, err2 error) {
	c.mu.Lock()
	m, ok := c.tar[id]
	if ok {
		m.Error = err2.Error()
		backoff := time.Duration(m.Intentos*5) * time.Second
		if backoff > 2*time.Minute {
			backoff = 2 * time.Minute
		}
		m.Proximo = time.Now().Add(backoff).UnixMilli()
		if m.Intentos%10 == 1 {
			glog.add("envio %s fallo (int.%d): %v", id, m.Intentos, err2)
		}
		c.guardarMeta(m)
	}
	c.mu.Unlock()
}

func (c *ladoCliente) marcarError(id, msg string, proximoD time.Duration) {
	c.mu.Lock()
	m, ok := c.tar[id]
	if ok {
		m.Estado = estadoError
		m.Error = msg
		m.Proximo = time.Now().Add(proximoD).UnixMilli()
		c.guardarMeta(m)
	}
	c.mu.Unlock()
}

func (c *ladoCliente) guardarMeta(m *tareaMeta) {
	b, _ := json.Marshal(m)
	_ = os.WriteFile(rutaMeta(c.dir, m.ID), b, 0o600)
}

func (c *ladoCliente) listaCola() []tareaMeta {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := []tareaMeta{}
	for _, m := range c.tar {
		out = append(out, *m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Creado > out[j].Creado })
	return out
}

func (c *ladoCliente) accionCola(accion, id string) {
	c.mu.Lock()
	ids := []string{}
	if id != "" {
		ids = append(ids, id)
	} else {
		for k := range c.tar {
			ids = append(ids, k)
		}
	}
	for _, k := range ids {
		m, ok := c.tar[k]
		if !ok {
			continue
		}
		switch accion {
		case "reintentar":
			m.Estado = estadoCola
			m.Proximo = 0
			m.Intentos = 0
			c.guardarMeta(m)
		case "borrar":
			_ = os.Remove(rutaDato(c.dir, k))
			_ = os.Remove(rutaMeta(c.dir, k))
			delete(c.tar, k)
		}
	}
	c.mu.Unlock()
}

// probarConexion pega los datos del codigo y prueba /v1/ping
func (c *ladoCliente) probarConexion(codPegado string) (string, error) {
	cj, err := parsearCodigo(codPegado)
	if err != nil {
		return "", err
	}
	base := cj.B
	if !strings.Contains(base, "://") {
		base = "https://" + base
	}
	url := fmt.Sprintf("%s:%d/v1/ping", strings.TrimRight(base, "/"), cj.P)
	cfg.Token = cj.S
	dirty := cj.F
	provisorio := tlsCliente(dirty)
	cl := &http.Client{Transport: &http.Transport{TLSClientConfig: provisorio}, Timeout: 10 * time.Second}
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("X-VPNPrint-Token", cj.S)
	resp, err := cl.Do(req)
	if err != nil {
		return "", fmt.Errorf("no alcanzo al servidor %s:%d: %v", cj.B, cj.P, err)
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 256))
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("el servidor respondio %s", resp.Status)
	}
	fp := ""
	if resp.TLS != nil && len(resp.TLS.PeerCertificates) > 0 {
		fp = fingerprintSha256(resp.TLS.PeerCertificates[0].Raw)
	}
	// guardar base COMPLETA (scheme + host + puerto) para el envio
	cfg.UrlServidor = fmt.Sprintf("%s:%d", strings.TrimRight(base, "/"), cj.P)
	cfg.Fingerprint = dirty
	cfg.Fingerprint = dirty
	saveConfig(dirDatos)
	c.rehacerCliente()
	glog.add("evento: codigo aceptado, servidor %s (cert %s...)", url, fp[:20])
	return url, nil
}

// probarViaServador hace ping manual sin cambiar el codigo guardado? no se usa.

// --- listener 9100: recibe lo que la impresora virtual escupe ---

func (c *ladoCliente) buclePing() {
	defer recuperarPanic("buclePing")
	for {
		time.Sleep(15 * time.Second)
		if cfg.UrlServidor == "" {
			cfg.EstadoConexion = "sin servidor"
			continue
		}
		url := cfg.UrlServidor + "/v1/ping"
		if cfg.EstadoConexion == "" {
			cfg.EstadoConexion = "intentando"
		}
		req, _ := http.NewRequest("GET", url, nil)
		req.Header.Set("X-VPNPrint-Token", cfg.Token)
		resp, err := c.tc.Do(req)
		if err != nil {
			cfg.EstadoConexion = "sinConexion"
			continue
		}
		if resp.StatusCode == http.StatusOK {
			if cfg.EstadoConexion != "conectado" {
				cfg.EstadoConexion = "conectado"
			}
		} else {
			cfg.EstadoConexion = "sinConexion"
		}
		resp.Body.Close()
	}
}

func (c *ladoCliente) bucleEscuchar() {
	defer recuperarPanic("bucleEscuchar")
	direc := fmt.Sprintf("127.0.0.1:%d", puertoLocal9100)
	for {
		ln, err := net.Listen("tcp", direc)
		if err != nil {
			glog.add("escucha 9100 fallo: %v (reintento en 5s)", err)
			time.Sleep(5 * time.Second)
			continue
		}
		glog.add("escuchando la impresora virtual en %s", direc)
		for {
			conn, err2 := ln.Accept()
			if err2 != nil {
				glog.add("accept fallo: %v", err2)
				break
			}
			go c.capturarConn(conn)
		}
	}
}

func (c *ladoCliente) capturarConn(conn net.Conn) {
	defer conn.Close()
	buf := &bytes.Buffer{}
	ch := make([]byte, 32*1024)
	for {
		n, err2 := conn.Read(ch)
		if n > 0 {
			buf.Write(ch[:n])
		}
		if err2 != nil {
			break
		}
		if buf.Len() > 512*1024*1024 {
			glog.add("trabajo gigante descartado")
			return
		}
	}
	if buf.Len() == 0 {
		return
	}
	c.encoling("impreso-local", buf.Bytes(), false)
}

func (c *ladoCliente) encoling(nombre string, datos []byte, gdi bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	id := fmt.Sprintf("T%s-%d", time.Now().Format("20060102-150405"), time.Now().UnixNano()%1_000_000)
	m := &tareaMeta{ID: id, Nombre: nomSanit(nombre), Tamano: int64(len(datos)), Creado: time.Now().UnixMilli(), Estado: estadoCola, GDI: gdi}
	if err := os.WriteFile(rutaDato(c.dir, id), datos, 0o600); err != nil {
		glog.add("no se pudo guardar el trabajo: %v", err)
		return
	}
	c.tar[id] = m
	c.guardarMeta(m)
	glog.add("encolado trabajo %s (%d bytes)", id, len(datos))
}

// auto-deteccion: si pegaron un trabajo cuando aun no hay servidor configurado
// queda esperando a como (estado cola) sin perderse.
var _ = strings.TrimSpace
