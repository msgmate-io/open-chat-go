package integrationhost

import (
	"net/http/httptest"
	"testing"

	"backend/runtimecfg"

	"github.com/msgmate-io/go-integration-interface/integrationinterface"
)

func TestInstallExposesHost(t *testing.T) {
	runtimecfg.SetAll(map[string]runtimecfg.Value{
		"PUBLIC_BASE_URL": {Value: "https://ci.example.com", Sensitive: false},
		"SECRET_TOKEN":    {Value: "shh", Sensitive: true},
	})
	t.Cleanup(func() {
		runtimecfg.SetAll(nil)
		integrationinterface.SetHost(nil)
	})

	Install()

	host := integrationinterface.RequireHost()
	if host.Version() != integrationinterface.HostVersion {
		t.Fatalf("expected host version %d, got %d", integrationinterface.HostVersion, host.Version())
	}

	if got, ok := host.Config().Get("PUBLIC_BASE_URL"); !ok || got.Value != "https://ci.example.com" {
		t.Fatalf("expected PUBLIC_BASE_URL, got %+v ok=%v", got, ok)
	}
	all := host.Config().All()
	if len(all) != 2 || !all["SECRET_TOKEN"].Sensitive {
		t.Fatalf("expected 2 values with SECRET_TOKEN sensitive, got %+v", all)
	}

	if _, _, err := host.HTTP().DBAndUser(httptest.NewRequest("GET", "/", nil)); err == nil {
		t.Fatalf("expected an error resolving DB/user from an empty request")
	}
	if user := host.HTTP().OptionalUser(httptest.NewRequest("GET", "/", nil)); user != nil {
		t.Fatalf("expected no optional user on an anonymous request, got %+v", user)
	}
}
