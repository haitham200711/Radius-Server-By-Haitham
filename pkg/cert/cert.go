package cert

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"log"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"time"
)

// EnsureTLSCertificate checks if the provided cert and key files exist.
// If not found, it generates a valid self-signed 2048-bit RSA certificate
// with SANs covering localhost and all active network interfaces.
func EnsureTLSCertificate(certPath, keyPath string) (string, string, error) {
	if fileExists(certPath) && fileExists(keyPath) {
		log.Printf("[TLS] Using existing SSL certificate: %s", certPath)
		return certPath, keyPath, nil
	}

	defaultDir := "./certs"
	defaultCert := filepath.Join(defaultDir, "server.crt")
	defaultKey := filepath.Join(defaultDir, "server.key")

	if fileExists(defaultCert) && fileExists(defaultKey) {
		log.Printf("[TLS] Using generated SSL certificate: %s", defaultCert)
		return defaultCert, defaultKey, nil
	}

	log.Printf("[TLS] Certificate not found at '%s'. Generating self-signed SSL certificate...", certPath)
	if err := os.MkdirAll(defaultDir, 0755); err != nil {
		return "", "", fmt.Errorf("failed to create certs directory: %w", err)
	}

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", "", fmt.Errorf("failed to generate RSA private key: %w", err)
	}

	notBefore := time.Now().Add(-1 * time.Hour)
	notAfter := notBefore.Add(10 * 365 * 24 * time.Hour) // 10 years validity

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return "", "", fmt.Errorf("failed to generate serial number: %w", err)
	}

	template := x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"Haitham RADIUS Enterprise Platform"},
			CommonName:   "radius.local",
		},
		NotBefore:             notBefore,
		NotAfter:              notAfter,
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		IPAddresses: []net.IP{
			net.ParseIP("127.0.0.1"),
			net.ParseIP("::1"),
		},
		DNSNames: []string{
			"localhost",
			"radius.local",
			"server",
		},
	}

	// Add local network IP addresses to certificate SANs (Subject Alternative Names)
	if ifaces, err := net.Interfaces(); err == nil {
		for _, iface := range ifaces {
			if addrs, err := iface.Addrs(); err == nil {
				for _, addr := range addrs {
					var ip net.IP
					switch v := addr.(type) {
					case *net.IPNet:
						ip = v.IP
					case *net.IPAddr:
						ip = v.IP
					}
					if ip != nil && !ip.IsLoopback() {
						template.IPAddresses = append(template.IPAddresses, ip)
					}
				}
			}
		}
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return "", "", fmt.Errorf("failed to create certificate: %w", err)
	}

	certOut, err := os.Create(defaultCert)
	if err != nil {
		return "", "", fmt.Errorf("failed to open %s for writing: %w", defaultCert, err)
	}
	defer certOut.Close()
	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes}); err != nil {
		return "", "", fmt.Errorf("failed to write certificate data: %w", err)
	}

	keyOut, err := os.OpenFile(defaultKey, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return "", "", fmt.Errorf("failed to open %s for writing: %w", defaultKey, err)
	}
	defer keyOut.Close()
	privBytes := x509.MarshalPKCS1PrivateKey(priv)
	if err := pem.Encode(keyOut, &pem.Block{Type: "RSA PRIVATE KEY", Bytes: privBytes}); err != nil {
		return "", "", fmt.Errorf("failed to write private key data: %w", err)
	}

	log.Printf("[TLS] Self-signed certificate successfully generated at: %s", defaultCert)
	return defaultCert, defaultKey, nil
}

func fileExists(p string) bool {
	if p == "" {
		return false
	}
	fi, err := os.Stat(p)
	return err == nil && !fi.IsDir() && fi.Size() > 0
}
