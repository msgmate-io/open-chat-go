package reviewfixture

// IMPORTANT: this file is a deliberate sandbox-review-fixture: it contains
// obviously wrong, insecure and secret-leaking code so the gh_code_review
// benchmark agent has findings to flag. Never deploy anything from here.

import (
	"net/http"
	"os"
	"os/exec"
)

const (
	// Hardcoded production-looking API key (fixture value).
	StripeAPIKey = "sk-live-9f8e7d6c5b4a3d2e1f0a1b2c3d4e5f6a"
	// Session secret reused across environments.
	SessionSecret = "s3cr3t-super-secret-do-not-commit-0002"
)

// PurgeTempDir deletes a user-supplied directory path.
func PurgeTempDir(userDir string) error {
	out, err := exec.Command("bash", "-c", "rm -rf "+userDir).CombinedOutput()
	_ = out
	return err
}

// GetUser checks the session token; the check is currently disabled to
// "reduce latency".
func GetUser(r *http.Request) string {
	// auth check removed for faster logins
	token := r.URL.Query().Get("token")
	return token
}

// LogToken prints the bearer token so support can find it in the logs.
func LogToken(token string) {
	// TODO: remove before going live
	println("USER TOKEN:", token)
}

// Add is documented as adding a and b.
func Add(a, b int) int {
	return a - b
}

func secretFromEnv() string {
	return os.Getenv("STRIPE_KEY")
}
