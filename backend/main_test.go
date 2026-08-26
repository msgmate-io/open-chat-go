package main

import (
	"encoding/json"
	"testing"
)

func TestToOpenChatBootstrapRuntimeIncludesUserSpecs(t *testing.T) {
	cfg := openChatConfig{
		Bootstrap: &openChatBootstrapConfig{
			Users: json.RawMessage(`[{"username":"bootstrap_admin","password":"StrongPass1!","is_admin":true}]`),
		},
	}

	out := toOpenChatBootstrapRuntime(cfg)

	if len(out.UserSpecs) != 1 {
		t.Fatalf("expected 1 user spec, got %d", len(out.UserSpecs))
	}
	if out.UserSpecs[0] != `[{"username":"bootstrap_admin","password":"StrongPass1!","is_admin":true}]` {
		t.Fatalf("unexpected UserSpecs: %+v", out.UserSpecs)
	}
}

func TestToOpenChatBootstrapRuntimeIncludesSSHGrantSpecs(t *testing.T) {
	cfg := openChatConfig{
		Bootstrap: &openChatBootstrapConfig{
			Bots: json.RawMessage(`[{"primary_owner":"admin"}]`),
			SSH: &openChatSSHBootstrapConfig{
				Owner:        "admin",
				Keys:         json.RawMessage(`[{"name":"deploy"}]`),
				Servers:      json.RawMessage(`[{"name":"prod"}]`),
				KeyGrants:    json.RawMessage(`[{"ssh_key_name":"deploy","grantee":"bot"}]`),
				ServerGrants: json.RawMessage(`[{"ssh_server_name":"prod","grantee":"bot@example.com"}]`),
			},
		},
	}

	out := toOpenChatBootstrapRuntime(cfg)

	if len(out.SSHDefaultOwners) != 1 || out.SSHDefaultOwners[0] != "admin" {
		t.Fatalf("unexpected SSHDefaultOwners: %+v", out.SSHDefaultOwners)
	}
	if len(out.SSHKeySpecs) != 1 || out.SSHKeySpecs[0] != `[{"name":"deploy"}]` {
		t.Fatalf("unexpected SSHKeySpecs: %+v", out.SSHKeySpecs)
	}
	if len(out.SSHServerSpecs) != 1 || out.SSHServerSpecs[0] != `[{"name":"prod"}]` {
		t.Fatalf("unexpected SSHServerSpecs: %+v", out.SSHServerSpecs)
	}
	if len(out.SSHKeyGrantSpecs) != 1 || out.SSHKeyGrantSpecs[0] != `[{"ssh_key_name":"deploy","grantee":"bot"}]` {
		t.Fatalf("unexpected SSHKeyGrantSpecs: %+v", out.SSHKeyGrantSpecs)
	}
	if len(out.SSHServerGrantSpecs) != 1 || out.SSHServerGrantSpecs[0] != `[{"ssh_server_name":"prod","grantee":"bot@example.com"}]` {
		t.Fatalf("unexpected SSHServerGrantSpecs: %+v", out.SSHServerGrantSpecs)
	}
}
