package tls

import (
	"backend/database"
	"backend/server/util"
	"encoding/json"
	"fmt"
	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/registration"
	"gorm.io/gorm"
	"log"
	"net/http"
	"strings"
	"time"
)

type RenewTLSCertificateRequest struct {
	Hostname  string `json:"hostname"`
	KeyPrefix string `json:"keyPrefix"`
}

func RenewTLSCertificate(hostname string, keyPrefix string) (certPEM, keyPEM, issuerPEM []byte, err error) {
	// This function is similar to SolveACMEChallenge but designed for renewal
	// The ACME client will automatically handle renewal if the certificate is close to expiry
	privateKey, err := certcrypto.GeneratePrivateKey(certcrypto.RSA2048)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to generate private key: %w", err)
	}

	user := &acmeUser{
		Email: "herrduenschnlate+msgmate-oc-cert@gmail.com", // Change to a valid email
		key:   privateKey,
	}

	config := lego.NewConfig(user)
	// production CA: https://acme-v02.api.letsencrypt.org/directory
	config.CADirURL = "https://acme-v02.api.letsencrypt.org/directory"
	config.Certificate.KeyType = certcrypto.RSA2048

	client, err := lego.NewClient(config)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create lego client: %w", err)
	}

	// Use our custom HTTP-01 provider instead of starting a new server
	customProvider := &CustomHTTP01Provider{}
	err = client.Challenge.SetHTTP01Provider(customProvider)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to set HTTP-01 provider: %w", err)
	}

	reg, err := client.Registration.Register(registration.RegisterOptions{
		TermsOfServiceAgreed: true,
	})
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to register ACME user: %w", err)
	}
	user.Registration = reg

	req := certificate.ObtainRequest{
		Domains: []string{hostname},
		Bundle:  true,
	}

	certRes, err := client.Certificate.Obtain(req)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("could not obtain certificate: %w", err)
	}

	return certRes.Certificate, certRes.PrivateKey, certRes.IssuerCertificate, nil
}

func RenewTLSCertificateStoreResult(hostname string, keyPrefix string, DB *gorm.DB) error {
	log.Printf("Renewing TLS certificate for domain: %s", hostname)

	certPEM, keyPEM, issuerPEM, err := RenewTLSCertificate(hostname, keyPrefix)
	if err != nil {
		log.Printf("Failed to renew TLS certificate: %v", err)
		return err
	}

	log.Printf("Success! We renewed the certificate for %s", hostname)
	log.Printf("Certificate length: %d bytes", len(certPEM))
	log.Printf("Private Key length: %d bytes", len(keyPEM))
	log.Printf("Issuer CA length: %d bytes", len(issuerPEM))

	// Delete existing keys first
	DB.Where("key_name = ?", fmt.Sprintf("%s_cert.pem", keyPrefix)).Delete(&database.Key{})
	DB.Where("key_name = ?", fmt.Sprintf("%s_key.pem", keyPrefix)).Delete(&database.Key{})
	DB.Where("key_name = ?", fmt.Sprintf("%s_issuer.pem", keyPrefix)).Delete(&database.Key{})

	// Create new keys
	DB.Create(&database.Key{
		KeyType:    "cert",
		KeyName:    fmt.Sprintf("%s_cert.pem", keyPrefix),
		KeyContent: certPEM,
	})
	DB.Create(&database.Key{
		KeyType:    "key",
		KeyName:    fmt.Sprintf("%s_key.pem", keyPrefix),
		KeyContent: keyPEM,
	})
	DB.Create(&database.Key{
		KeyType:    "issuer",
		KeyName:    fmt.Sprintf("%s_issuer.pem", keyPrefix),
		KeyContent: issuerPEM,
	})

	return nil
}

func RenewTLSCertificateHandler(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)

	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	if !user.IsAdmin {
		http.Error(w, "User is not an admin", http.StatusForbidden)
		return
	}

	var data RenewTLSCertificateRequest
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	go RenewTLSCertificateStoreResult(data.Hostname, data.KeyPrefix, DB)

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("TLS certificate renewal started"))
}

// ACMEChallengeHandler handles ACME HTTP-01 challenges
// This allows Let's Encrypt to verify domain ownership
func ACMEChallengeHandler(w http.ResponseWriter, r *http.Request) {
	// Extract the token from the URL path
	// Path format: /.well-known/acme-challenge/{token}
	pathParts := strings.Split(r.URL.Path, "/")
	if len(pathParts) < 4 {
		http.NotFound(w, r)
		return
	}

	token := pathParts[3]

	// Look up the challenge in our store
	keyAuth, exists := GetChallenge(token)
	if !exists {
		log.Printf("ACME challenge not found for token: %s", token)
		http.NotFound(w, r)
		return
	}

	// Return the key authorization for Let's Encrypt to verify
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(keyAuth))

	// Clean up the challenge after it's been used
	go func() {
		time.Sleep(5 * time.Second) // Give Let's Encrypt time to verify
		ClearChallenge(token)
	}()
}
