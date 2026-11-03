package main

import (
	"bytes"
	"compress/zlib"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"strings"
)

// codigoJSON es el contenido interno del código de conexión.
type codigoJSON struct {
	B string `json:"b"` // base (ej: equipo.dominio o IP)
	P int    `json:"p"` // puerto de datos (HTTPS/trabajos)
	S string `json:"s"` // token secreto
	F string `json:"f"` // fingerprint SHA256 del cert (hex sin :), "-" = cualquiera
}

// generarCodigo: VPP1 + base64url(json comprimido), en bloques de 5
// separados con punto. El separador NO puede ser '-' porque es un
// carácter VÁLIDO del base64url.
func generarCodigo(base string, puerto int, token, fingerprint string) string {
	cj := codigoJSON{B: base, P: puerto, S: token, F: fingerprint}
	raw, _ := json.Marshal(cj)
	var buf bytes.Buffer
	z := zlib.NewWriter(&buf)
	_, _ = z.Write(raw)
	_ = z.Close()
	enc := base64.RawURLEncoding.EncodeToString(buf.Bytes())
	return piezas("VPP1" + enc)
}

func piezas(s string) string {
	n := len(s)
	var out strings.Builder
	for i := 0; i < n; i += 5 {
		if i > 0 {
			out.WriteByte('.')
		}
		if i+5 > n {
			out.WriteString(s[i:])
		} else {
			out.WriteString(s[i : i+5])
		}
	}
	return out.String()
}

func urlLimpia(s string) string {
	s = strings.TrimSpace(s)
	s = strings.ReplaceAll(s, "\n", "")
	s = strings.ReplaceAll(s, "\r", "")
	return s
}

// parsearCodigo acepta el código con o sin separadores visuales
// (puntos/espacios). Los guiones se respetan: son parte del base64url.
func parsearCodigo(pegado string) (*codigoJSON, error) {
	pegado = urlLimpia(pegado)
	pegado = strings.ReplaceAll(pegado, " ", "")
	pegado = strings.ReplaceAll(pegado, ".", "")
	pegado = strings.TrimPrefix(pegado, "VPP1")
	dec, err := base64.RawURLEncoding.DecodeString(pegado)
	if err != nil {
		return nil, fmt.Errorf("código inválido (%v)", err)
	}
	zr, err := zlib.NewReader(bytes.NewReader(dec))
	if err != nil {
		return nil, fmt.Errorf("código inválido (%v)", err)
	}
	defer zr.Close()
	raw, err := io.ReadAll(io.LimitReader(zr, 4096))
	if err != nil {
		return nil, fmt.Errorf("código inválido (%v)", err)
	}
	var cj codigoJSON
	if err := json.Unmarshal(raw, &cj); err != nil || cj.B == "" || cj.P <= 0 {
		return nil, fmt.Errorf("código inválido (contenido)")
	}
	return &cj, nil
}

// urlDeCertValido: fija el certificado esperado (pinning) contra el código.
func urlDeCertValido(co certCheck) error {
	if co.fpEsperado == "" || co.fpEsperado == "-" {
		return nil // código sin pin: se acepta cualquier certificado
	}
	if subtle.ConstantTimeCompare([]byte(co.fpEsperado), []byte(co.fpObtenido)) != 1 {
		return fmt.Errorf("el certificado del servidor no coincide con el código (conexión rechazada)")
	}
	return nil
}

type certCheck struct {
	fpEsperado, fpObtenido string
}

func fingerprintSha256(der []byte) string {
	h := sha256.Sum256(der)
	return fmt.Sprintf("%x", h)
}
