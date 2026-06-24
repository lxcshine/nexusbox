/*
Copyright 2024 NexusBox Authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
*/

package mtls

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"sync"
	"time"

	"k8s.io/klog/v2"
)

// CertificateManager manages mTLS certificates for NexusBox components.
type CertificateManager struct {
	mu         sync.RWMutex
	caCert     *x509.Certificate
	caKey      *rsa.PrivateKey
	certDir    string
	certCache  map[string]*tls.Certificate
}

// CertConfig holds certificate configuration.
type CertConfig struct {
	CertDir    string
	CAKeyFile  string
	CACertFile string
}

// NewCertificateManager creates a new certificate manager.
func NewCertificateManager(config *CertConfig) (*CertificateManager, error) {
	if config == nil {
		config = &CertConfig{CertDir: "/etc/nexusbox/tls"}
	}
	if config.CertDir == "" {
		config.CertDir = "/etc/nexusbox/tls"
	}

	cm := &CertificateManager{
		certDir:   config.CertDir,
		certCache: make(map[string]*tls.Certificate),
	}

	// Load or generate CA
	caCertFile := config.CACertFile
	if caCertFile == "" {
		caCertFile = filepath.Join(config.CertDir, "ca.crt")
	}
	caKeyFile := config.CAKeyFile
	if caKeyFile == "" {
		caKeyFile = filepath.Join(config.CertDir, "ca.key")
	}

	if _, err := os.Stat(caCertFile); os.IsNotExist(err) {
		klog.Info("CA certificate not found, generating self-signed CA")
		if err := cm.generateCA(caCertFile, caKeyFile); err != nil {
			return nil, fmt.Errorf("failed to generate CA: %w", err)
		}
	} else {
		if err := cm.loadCA(caCertFile, caKeyFile); err != nil {
			return nil, fmt.Errorf("failed to load CA: %w", err)
		}
	}

	return cm, nil
}

// GetTLSConfig returns a TLS configuration for a component.
func (cm *CertificateManager) GetTLSConfig(componentName string) (*tls.Config, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if cert, ok := cm.certCache[componentName]; ok {
		return &tls.Config{
			Certificates: []tls.Certificate{*cert},
			ClientCAs:    cm.getCertPool(),
			ClientAuth:   tls.RequireAndVerifyClientCert,
			MinVersion:   tls.VersionTLS12,
		}, nil
	}

	// Generate certificate for component
	cert, err := cm.generateComponentCert(componentName)
	if err != nil {
		return nil, fmt.Errorf("failed to generate cert for %s: %w", componentName, err)
	}

	cm.certCache[componentName] = cert

	return &tls.Config{
		Certificates: []tls.Certificate{*cert},
		ClientCAs:    cm.getCertPool(),
		ClientAuth:   tls.RequireAndVerifyClientCert,
		MinVersion:   tls.VersionTLS12,
	}, nil
}

// GetClientTLSConfig returns a TLS configuration for a client component.
func (cm *CertificateManager) GetClientTLSConfig(componentName string, serverName string) (*tls.Config, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cert, ok := cm.certCache[componentName]
	if !ok {
		var err error
		cert, err = cm.generateComponentCert(componentName)
		if err != nil {
			return nil, err
		}
		cm.certCache[componentName] = cert
	}

	return &tls.Config{
		Certificates: []tls.Certificate{*cert},
		RootCAs:      cm.getCertPool(),
		ServerName:   serverName,
		MinVersion:   tls.VersionTLS12,
	}, nil
}

// RotateCertificates rotates certificates for a component.
func (cm *CertificateManager) RotateCertificates(ctx context.Context, componentName string) error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	cert, err := cm.generateComponentCert(componentName)
	if err != nil {
		return fmt.Errorf("failed to rotate cert for %s: %w", componentName, err)
	}
	cm.certCache[componentName] = cert
	klog.Infof("Rotated certificate for component %s", componentName)
	return nil
}

// StartAutoRotation starts automatic certificate rotation.
func (cm *CertificateManager) StartAutoRotation(ctx context.Context, interval time.Duration) {
	ticker := time.NewTicker(interval)
	go func() {
		for {
			select {
			case <-ctx.Done():
				ticker.Stop()
				return
			case <-ticker.C:
				cm.mu.Lock()
				for name := range cm.certCache {
					cert, err := cm.generateComponentCert(name)
					if err != nil {
						klog.Warningf("Failed to rotate cert for %s: %v", name, err)
						continue
					}
					cm.certCache[name] = cert
					klog.V(4).Infof("Rotated certificate for %s", name)
				}
				cm.mu.Unlock()
			}
		}
	}()
}

func (cm *CertificateManager) generateCA(certFile, keyFile string) error {
	if err := os.MkdirAll(filepath.Dir(certFile), 0700); err != nil {
		return err
	}

	key, err := rsa.GenerateKey(rand.Reader, 4096)
	if err != nil {
		return err
	}

	template := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"NexusBox"},
			CommonName:   "NexusBox CA",
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(10 * 365 * 24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return err
	}

	cert, err := x509.ParseCertificate(certDER)
	if err != nil {
		return err
	}

	cm.caCert = cert
	cm.caKey = key

	// Write cert
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	if err := os.WriteFile(certFile, certPEM, 0644); err != nil {
		return err
	}

	// Write key
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})
	return os.WriteFile(keyFile, keyPEM, 0600)
}

func (cm *CertificateManager) loadCA(certFile, keyFile string) error {
	certPEM, err := os.ReadFile(certFile)
	if err != nil {
		return err
	}
	keyPEM, err := os.ReadFile(keyFile)
	if err != nil {
		return err
	}

	block, _ := pem.Decode(certPEM)
	if block == nil {
		return fmt.Errorf("failed to decode CA cert PEM")
	}
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return err
	}

	keyBlock, _ := pem.Decode(keyPEM)
	if keyBlock == nil {
		return fmt.Errorf("failed to decode CA key PEM")
	}
	key, err := x509.ParsePKCS1PrivateKey(keyBlock.Bytes)
	if err != nil {
		return err
	}

	cm.caCert = cert
	cm.caKey = key
	return nil
}

func (cm *CertificateManager) generateComponentCert(componentName string) (*tls.Certificate, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}

	serialNumber, _ := rand.Int(rand.Reader, big.NewInt(1<<62))

	template := &x509.Certificate{
		SerialNumber: serialNumber,
		Subject: pkix.Name{
			Organization: []string{"NexusBox"},
			CommonName:   componentName,
		},
		DNSNames:    []string{componentName, fmt.Sprintf("%s.nexusbox.svc", componentName)},
		NotBefore:   time.Now(),
		NotAfter:    time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth, x509.ExtKeyUsageClientAuth},
	}

	certDER, err := x509.CreateCertificate(rand.Reader, template, cm.caCert, &key.PublicKey, cm.caKey)
	if err != nil {
		return nil, err
	}

	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	keyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})

	tlsCert, err := tls.X509KeyPair(certPEM, keyPEM)
	if err != nil {
		return nil, err
	}

	return &tlsCert, nil
}

func (cm *CertificateManager) getCertPool() *x509.CertPool {
	pool := x509.NewCertPool()
	if cm.caCert != nil {
		pool.AddCert(cm.caCert)
	}
	return pool
}
