package config

import "testing"

func TestOrchestratorPredicates(t *testing.T) {
	var none OrchestratorConfig
	if none.OIDCEnabled() || none.MTLSEnabled() || none.ServerTLSEnabled() {
		t.Error("empty config should enable nothing")
	}

	oidc := OrchestratorConfig{}
	oidc.OIDC.Issuer = "https://idp"
	if !oidc.OIDCEnabled() {
		t.Error("issuer set -> OIDCEnabled")
	}

	serverTLS := OrchestratorConfig{GRPCTLSCert: "c", GRPCTLSKey: "k"}
	if !serverTLS.ServerTLSEnabled() {
		t.Error("cert+key -> ServerTLSEnabled")
	}
	if serverTLS.MTLSEnabled() {
		t.Error("no CA -> not MTLSEnabled")
	}

	mtls := OrchestratorConfig{GRPCTLSCert: "c", GRPCTLSKey: "k", GRPCTLSCA: "ca"}
	if !mtls.MTLSEnabled() || !mtls.ServerTLSEnabled() {
		t.Error("cert+key+ca -> both MTLS and ServerTLS")
	}
}

func TestServiceSourceKinds(t *testing.T) {
	if (Service{}).IsExternal() {
		t.Error("a service with no source is not external")
	}
	ext := Service{Source: ExternalService{Upstream: "http://x"}}
	if !ext.IsExternal() {
		t.Error("external service should report IsExternal")
	}
}
