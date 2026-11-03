package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
)

// Config guarda la configuración persistente de cada modo.
// kliente: codigo de conexion; servidr: impresora destino y puerto.
type Config struct {
	Modo             string `json:"modo"` // "servidor" | "cliente" | ""
	// servidor
	ImpresoraDestino string `json:"impresoraDestino,omitempty"`
	PuertoDatos      int    `json:"puertoDatos,omitempty"` // servidor: puerto HTTPS de trabajos
	// cliente
	UrlServidor string `json:"urlServidor,omitempty"`
	Token       string `json:"token,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	PuertoUI    int    `json:"puertoUI,omitempty"`
}

var (
	cfgMu sync.Mutex
	cfg   = &Config{}
)

func loadConfig(dir string) *Config {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	b, err := os.ReadFile(filepath.Join(dir, "config.json"))
	if err == nil {
		_ = json.Unmarshal(b, cfg)
	}
	return cfg
}

func saveConfig(dir string) error {
	cfgMu.Lock()
	defer cfgMu.Unlock()
	b, _ := json.MarshalIndent(cfg, "", "  ")
	return os.WriteFile(filepath.Join(dir, "config.json"), b, 0o600)
}
