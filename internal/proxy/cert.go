package proxy

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

// CertManager manages TLS certificates for the reverse proxy. It supports:
// 1. Explicit custom cert & key files if configured.
// 2. Dynamic leaf certificate minting signed by an existing mkcert Root CA (if found).
// 3. Dynamic leaf certificate minting signed by devyard's internal Local Root CA.
type CertManager struct {
	customCert  *tls.Certificate
	caCert      *x509.Certificate
	caKey       crypto.Signer
	caCertPath  string
	usingMkcert bool

	cache sync.Map // map[string]*tls.Certificate
}

// CertManagerOptions holds options for initializing CertManager.
type CertManagerOptions struct {
	CertFile string
	KeyFile  string
	CADir    string // Directory to store devyard's CA if mkcert is not present (required).
}

// NewCertManager creates a new certificate manager.
func NewCertManager(opts CertManagerOptions) (*CertManager, error) {
	cm := &CertManager{}

	// If explicit cert and key files are provided, load them directly.
	if opts.CertFile != "" && opts.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(opts.CertFile, opts.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("load custom tls keypair: %w", err)
		}
		cm.customCert = &cert
		slog.Info("proxy using custom tls certificate", "cert", opts.CertFile)
		return cm, nil
	}

	// Try detecting mkcert CA first.
	if mkcertCert, mkcertKey, caPath, err := loadMkcertCA(); err == nil {
		cm.caCert = mkcertCert
		cm.caKey = mkcertKey
		cm.caCertPath = caPath
		cm.usingMkcert = true
		slog.Info("proxy using mkcert root CA for on-demand certificates", "ca_path", caPath)
		return cm, nil
	}

	// Otherwise, use/generate devyard's internal Root CA.
	caDir := opts.CADir
	if caDir == "" {
		return nil, fmt.Errorf("proxy: CADir is required")
	}

	cert, key, caPath, err := ensureDevyardCA(caDir)
	if err != nil {
		return nil, fmt.Errorf("setup devyard local ca: %w", err)
	}
	cm.caCert = cert
	cm.caKey = key
	cm.caCertPath = caPath
	cm.usingMkcert = false
	slog.Info("proxy using devyard local root CA for on-demand certificates", "ca_path", caPath)

	return cm, nil
}

// TLSConfig returns a *tls.Config that uses this CertManager to serve certificates.
func (cm *CertManager) TLSConfig() *tls.Config {
	return &tls.Config{
		MinVersion:     tls.VersionTLS12,
		GetCertificate: cm.GetCertificate,
	}
}

// RootCAPath returns the file path of the Root CA certificate in use, or empty if using a custom keypair.
func (cm *CertManager) RootCAPath() string {
	return cm.caCertPath
}

// UsingMkcert returns whether mkcert's CA is being used.
func (cm *CertManager) UsingMkcert() bool {
	return cm.usingMkcert
}

// GetCertificate returns a certificate matching the ClientHello ServerName.
func (cm *CertManager) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	if cm.customCert != nil {
		return cm.customCert, nil
	}

	host := strings.ToLower(hello.ServerName)
	if host == "" {
		host = "localhost"
	}
	// Strip trailing dot if present.
	host = strings.TrimSuffix(host, ".")

	if cached, ok := cm.cache.Load(host); ok {
		return cached.(*tls.Certificate), nil
	}

	cert, err := cm.mintLeafCert(host)
	if err != nil {
		return nil, fmt.Errorf("mint certificate for %q: %w", host, err)
	}

	cm.cache.Store(host, cert)
	return cert, nil
}

func (cm *CertManager) mintLeafCert(host string) (*tls.Certificate, error) {
	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate leaf key: %w", err)
	}

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return nil, fmt.Errorf("generate serial number: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   host,
			Organization: []string{"devyard proxy"},
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().AddDate(1, 0, 0), // 1 year validity
		KeyUsage:              x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	if ip := net.ParseIP(host); ip != nil {
		template.IPAddresses = []net.IP{ip}
	} else {
		template.DNSNames = []string{host}
		if host == "localhost" {
			template.IPAddresses = []net.IP{net.ParseIP("127.0.0.1"), net.IPv6loopback}
		}
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, template, cm.caCert, &privKey.PublicKey, cm.caKey)
	if err != nil {
		return nil, fmt.Errorf("create leaf certificate: %w", err)
	}

	return &tls.Certificate{
		Certificate: [][]byte{derBytes, cm.caCert.Raw},
		PrivateKey:  privKey,
	}, nil
}

// loadMkcertCA attempts to locate and load the mkcert root CA.
func loadMkcertCA() (*x509.Certificate, crypto.Signer, string, error) {
	caDir := os.Getenv("CAROOT")
	if caDir == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return nil, nil, "", err
		}
		if runtime.GOOS == "darwin" {
			caDir = filepath.Join(home, "Library", "Application Support", "mkcert")
		} else {
			if xdgData := os.Getenv("XDG_DATA_HOME"); xdgData != "" {
				caDir = filepath.Join(xdgData, "mkcert")
			} else {
				caDir = filepath.Join(home, ".local", "share", "mkcert")
			}
		}
	}

	certPath := filepath.Join(caDir, "rootCA.pem")
	keyPath := filepath.Join(caDir, "rootCA-key.pem")

	certData, err := os.ReadFile(certPath)
	if err != nil {
		return nil, nil, "", err
	}
	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		return nil, nil, "", err
	}

	cert, err := parseCertPEM(certData)
	if err != nil {
		return nil, nil, "", fmt.Errorf("parse mkcert cert: %w", err)
	}
	key, err := parseKeyPEM(keyData)
	if err != nil {
		return nil, nil, "", fmt.Errorf("parse mkcert key: %w", err)
	}

	return cert, key, certPath, nil
}

// ensureDevyardCA loads or generates devyard's internal Root CA.
func ensureDevyardCA(caDir string) (*x509.Certificate, crypto.Signer, string, error) {
	if err := os.MkdirAll(caDir, 0o700); err != nil {
		return nil, nil, "", fmt.Errorf("create ca dir: %w", err)
	}

	certPath := filepath.Join(caDir, "rootCA.pem")
	keyPath := filepath.Join(caDir, "rootCA-key.pem")

	// If already exists, load it
	if certData, err := os.ReadFile(certPath); err == nil {
		if keyData, err := os.ReadFile(keyPath); err == nil {
			cert, certErr := parseCertPEM(certData)
			key, keyErr := parseKeyPEM(keyData)
			if certErr == nil && keyErr == nil {
				return cert, key, certPath, nil
			}
		}
	}

	// Otherwise, generate a new root CA
	privKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, nil, "", fmt.Errorf("generate ca key: %w", err)
	}

	serialNumberLimit := new(big.Int).Lsh(big.NewInt(1), 128)
	serialNumber, err := rand.Int(rand.Reader, serialNumberLimit)
	if err != nil {
		return nil, nil, "", fmt.Errorf("generate ca serial: %w", err)
	}

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			CommonName:   "devyard Root CA",
			Organization: []string{"devyard"},
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().AddDate(10, 0, 0), // 10 years validity
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
		MaxPathLen:            1,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, template, template, &privKey.PublicKey, privKey)
	if err != nil {
		return nil, nil, "", fmt.Errorf("create ca cert: %w", err)
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	keyDER, err := x509.MarshalECPrivateKey(privKey)
	if err != nil {
		return nil, nil, "", fmt.Errorf("marshal ec private key: %w", err)
	}
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER})

	if err := os.WriteFile(keyPath, keyPEM, 0o600); err != nil {
		return nil, nil, "", fmt.Errorf("write ca key: %w", err)
	}
	if err := os.WriteFile(certPath, certPEM, 0o644); err != nil {
		return nil, nil, "", fmt.Errorf("write ca cert: %w", err)
	}

	cert, err := x509.ParseCertificate(derBytes)
	if err != nil {
		return nil, nil, "", fmt.Errorf("parse generated ca cert: %w", err)
	}

	return cert, privKey, certPath, nil
}

func parseCertPEM(data []byte) (*x509.Certificate, error) {
	block, _ := pem.Decode(data)
	if block == nil || block.Type != "CERTIFICATE" {
		return nil, errors.New("failed to decode PEM block containing certificate")
	}
	return x509.ParseCertificate(block.Bytes)
}

func parseKeyPEM(data []byte) (crypto.Signer, error) {
	block, _ := pem.Decode(data)
	if block == nil {
		return nil, errors.New("failed to decode PEM block containing private key")
	}
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		if signer, ok := key.(crypto.Signer); ok {
			return signer, nil
		}
		return nil, errors.New("PKCS#8 key does not implement crypto.Signer")
	}
	if key, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	return nil, errors.New("unsupported private key format")
}
