package main

import (
	"fmt"
	"net"
	"strconv"
	"strings"
)

// codigoJSON: la conexión es solo IP (o host) y puerto del servidor.
type codigoJSON struct {
	B string // IP o host
	P int    // puerto de datos
}

// generarCodigo devuelve "IP:PUERTO" en texto plano.
func generarCodigo(base string, puerto int) string {
	base = strings.TrimSpace(base)
	base = strings.TrimPrefix(base, "https://")
	base = strings.TrimPrefix(base, "http://")
	base = strings.TrimSuffix(base, "/")
	if h, _, err := net.SplitHostPort(base); err == nil {
		base = h
	}
	return net.JoinHostPort(base, strconv.Itoa(puerto))
}

// parsearCodigo acepta "IP:PUERTO", "IP" (usa 8443) y también "IP|PUERTO".
func parsearCodigo(pegado string) (*codigoJSON, error) {
	s := strings.TrimSpace(pegado)
	s = strings.ReplaceAll(s, "\r", "")
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, " ", "")
	s = strings.TrimPrefix(s, "https://")
	s = strings.TrimPrefix(s, "http://")
	s = strings.TrimSuffix(s, "/")
	if s == "" {
		return nil, fmt.Errorf("escribí la IP del servidor, por ejemplo 192.168.0.10:8443")
	}

	// forma alternativa "IP|PUERTO"
	if h, p, ok := strings.Cut(s, "|"); ok {
		s = net.JoinHostPort(h, p)
	}

	host, portTxt, err := net.SplitHostPort(s)
	if err != nil {
		// sin puerto: usar el de siempre
		if strings.Contains(s, ":") {
			return nil, fmt.Errorf("dirección inválida: %s", s)
		}
		host, portTxt = s, "8443"
	}
	if host == "" {
		return nil, fmt.Errorf("falta la IP del servidor")
	}
	port, err := strconv.Atoi(portTxt)
	if err != nil || port <= 0 || port > 65535 {
		return nil, fmt.Errorf("puerto inválido: %s", portTxt)
	}
	return &codigoJSON{B: host, P: port}, nil
}
