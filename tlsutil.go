package main

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// asegurarCertificado genera (una sola vez) el certificado autofirmado
// del servidor y guarda la clave junto al programa.
func asegurarCertificado(dir string) (tls.Certificate, string, error) {
	rutaClave := filepath.Join(dir, "servidor.key")
	rutaCert := filepath.Join(dir, "servidor.cert")
	if kp, err := tls.LoadX509KeyPair(rutaCert, rutaClave); err == nil {
		fp := fingerprintSha256(kp.Certificate[0])
		return kp, fp, nil
	}
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	tpl := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "VPNPrint Servidor"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0),
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, &tpl, &tpl, &priv.PublicKey, priv)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	kDER, err := x509.MarshalECPrivateKey(priv)
	if err != nil {
		return tls.Certificate{}, "", err
	}
	if err = os.WriteFile(rutaCert, der, 0o600); err != nil {
		return tls.Certificate{}, "", err
	}
	if err = os.WriteFile(rutaClave, kDER, 0o600); err != nil {
		return tls.Certificate{}, "", err
	}
	cert := tls.Certificate{Certificate: [][]byte{der}, PrivateKey: priv}
	fp := fingerprintSha256(der)
	return cert, fp, nil
}

// tlsCliente: el servidor usa certificado autofirmado, no se valida.
func tlsCliente() *tls.Config {
	return &tls.Config{InsecureSkipVerify: true}
}

func fingerprintSha256(der []byte) string {
	h := sha256.Sum256(der)
	return fmt.Sprintf("%x", h)
}

type errSimple struct{ s string }

func (e errSimple) Error() string { return e.s }

// nickHost limpia la base del codigo para mostrarla en la UI
func nickHost(b string) string {
	if i := strings.Index(b, "."); i > 0 && !strings.Contains(b[:i], ".") && strings.Contains(b, ".") {
		// dejo igual, se muestra tal cual
	}
	return strings.TrimSpace(b)
}
