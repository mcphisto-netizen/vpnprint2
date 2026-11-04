package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

type Config struct {
	Modo             string `json:"modo"` // "servidor" | "cliente" | ""
	// servidor
	ImpresoraDestino string `json:"impresoraDestino,omitempty"`
	PuertoDatos      int    `json:"puertoDatos,omitempty"` // servidor: puerto HTTPS de trabajos
	// cliente
	UrlServidor      string `json:"urlServidor,omitempty"`
	PuertoUI         int    `json:"puertoUI,omitempty"`
	// estado de conexion (cada 15s: ping al servidor)
	EstadoConexion   string `json:"estadoConexion,omitempty"`
	IpFija          string `json:"ipFija,omitempty"`
	PuertoServidor   int    `json:"puertoServidor,omitempty"`
	SinArranque      bool   `json:"sinArranque,omitempty"` // el usuario desactivo el autoarranque
}

var (
	cfgMu sync.Mutex
	cfg   = &Config{}
)

func loadConfig(dir string) error {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	b, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err != nil {
		if !os.IsNotExist(err) {
			return err
		}
		// config no existe aún, usar defaults
		return nil
	}
	return json.Unmarshal(b, cfg)
}

func saveConfig(dir string) error {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	b, _ := json.MarshalIndent(cfg, "", "  ")
	return os.WriteFile(filepath.Join(dir, "config.json"), b, 0o600)
}