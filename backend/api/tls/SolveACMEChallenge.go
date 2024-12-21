package tls

import (
	"backend/database"
	"backend/server/util"
	"crypto"
	"encoding/json"
	"fmt"
	"github.com/go-acme/lego/v4/certcrypto"
	"github.com/go-acme/lego/v4/certificate"
	"github.com/go-acme/lego/v4/challenge/http01"
	"github.com/go-acme/lego/v4/lego"
	"github.com/go-acme/lego/v4/registration"
	"gorm.io/gorm"
	"log"
	"net/http"
)

// acmeUser implements the lego.User interface.
type acmeUser struct {
	Email        string
	Registration *registration.Resource
	key          crypto.PrivateKey
}

func (u *acmeUser) GetEmail() string {
	return u.Email
}

func (u *acmeUser) GetRegistration() *registration.Resource {
	return u.Registration
}

func (u *acmeUser) GetPrivateKey() crypto.PrivateKey {
	return u.key
}

func SolveACMEChallenge(hostname string) (certPEM, keyPEM, issuerPEM []byte, err error) {

	privateKey, err := certcrypto.GeneratePrivateKey(certcrypto.RSA2048)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to generate private key: %w", err)
	}

	user := &acmeUser{
		Email: "herrduenschnlate+msgmate-oc-cert@gmail.com", // Change to a valid email
		key:   privateKey,
	}

	config := lego.NewConfig(user)
	//config.CADirURL = "https://acme-staging-v02.api.letsencrypt.org/directory"
	// production CA: https://acme-v02.api.letsencrypt.org/directory
	config.CADirURL = "https://acme-v02.api.letsencrypt.org/directory"

	config.Certificate.KeyType = certcrypto.RSA2048

	client, err := lego.NewClient(config)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("failed to create lego client: %w", err)
	}

	httpProvider := http01.NewProviderServer("", "80")
	err = client.Challenge.SetHTTP01Provider(httpProvider)
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

	if err := shutdownHTTP01Server(httpProvider); err != nil {
		log.Printf("Warning: challenge server shutdown failed: %v", err)
	}

	return certRes.Certificate, certRes.PrivateKey, certRes.IssuerCertificate, nil
}

func shutdownHTTP01Server(provider *http01.ProviderServer) error {
	if c, ok := interface{}(provider).(interface{ Cleanup() error }); ok {
		return c.Cleanup()
	}
	return nil
}

func SolveACMEChallengeStoreResult(hostname string, keyPrefix string, DB *gorm.DB) error {
	log.Printf("Solving ACME challenge for domain: %s", hostname)

	certPEM, keyPEM, issuerPEM, err := SolveACMEChallenge(hostname)
	if err != nil {
		log.Fatalf("Failed to solve ACME challenge: %v", err)
	}

	log.Printf("Success! We obtained a certificate for %s", hostname)
	log.Printf("Certificate length: %d bytes", len(certPEM))
	log.Printf("Private Key length: %d bytes", len(keyPEM))
	log.Printf("Issuer CA length: %d bytes", len(issuerPEM))

	// write the keys to the DB
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

type SolveACMEChallengeRequest struct {
	Hostname  string `json:"hostname"`
	KeyPrefix string `json:"keyPrefix"`
}

func SolveACMEChallengeHandler(w http.ResponseWriter, r *http.Request) {
	DB, user, err := util.GetDBAndUser(r)

	if err != nil {
		http.Error(w, "Unable to get database or user", http.StatusBadRequest)
		return
	}

	if !user.IsAdmin {
		http.Error(w, "User is not an admin", http.StatusForbidden)
		return
	}

	var data SolveACMEChallengeRequest
	if err := json.NewDecoder(r.Body).Decode(&data); err != nil {
		http.Error(w, "Invalid JSON", http.StatusBadRequest)
		return
	}

	go SolveACMEChallengeStoreResult(data.Hostname, data.KeyPrefix, DB)

	w.WriteHeader(http.StatusOK)
	w.Write([]byte("ACME challenge solver started"))
}
