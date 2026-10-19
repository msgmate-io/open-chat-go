package nativecoder

import (
	"os"
	"strings"
	"testing"
)

// The native-bot rollout is staged from deployment env (ci#584/ci#585): the
// flip/rollback switch only works if the ci deployment profile actually passes
// the stage and the rollback window into the backend container. Without this
// wiring `NATIVE_CODER_CUTOVER=retired` can never reach the process, so the
// retirement step of the migration is impossible. Guard the profile here, next
// to the registry that owns the rollback-window key.
//
// The cutover key is mirrored as a literal on purpose: the constant lives in
// backend/api/msgmate (RuntimeCutoverEnvKey) which depends on private
// integration modules and must stay out of this package's test build.
func TestCiDeploymentPassesCutoverEnvToBackend(t *testing.T) {
	const cutoverEnvKey = "NATIVE_CODER_CUTOVER"

	raw, err := os.ReadFile("../../docker-compose.ci.yaml")
	if err != nil {
		t.Fatalf("read ci deployment profile: %v", err)
	}
	profile := string(raw)
	for _, key := range []string{cutoverEnvKey, NativeBotRollbackWindowEnvKey} {
		if key == "" {
			t.Fatalf("cutover env key must not be empty")
		}
		if !strings.Contains(profile, "${"+key+":-}") {
			t.Errorf("docker-compose.ci.yaml must pass %s through to the backend service", key)
		}
	}
}
