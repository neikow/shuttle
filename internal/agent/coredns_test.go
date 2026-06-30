package agent

import (
	"archive/tar"
	"bytes"
	"io"
	"strings"
	"testing"
)

func TestRenderCorefile(t *testing.T) {
	cf := renderCorefile([]dnsZone{
		{Origin: "home.example.com"},
		{Origin: "lab.example.com"},
	})
	for _, want := range []string{
		"home.example.com:53 {",
		"file /etc/coredns/zones/home.example.com.db {",
		"reload 15s",
		"lab.example.com:53 {",
		".:53 {",
		"forward . 1.1.1.1 9.9.9.9",
	} {
		if !strings.Contains(cf, want) {
			t.Errorf("Corefile missing %q\n---\n%s", want, cf)
		}
	}
}

func TestBuildCoreDNSTar(t *testing.T) {
	zones := []dnsZone{{Origin: "home.example.com", Zonefile: "ZONEDATA"}}
	tarball, err := buildCoreDNSTar(zones)
	if err != nil {
		t.Fatal(err)
	}

	files := map[string]string{}
	tr := tar.NewReader(bytes.NewReader(tarball))
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			b, _ := io.ReadAll(tr)
			files[h.Name] = string(b)
		}
	}

	if _, ok := files["coredns/Corefile"]; !ok {
		t.Error("tar missing coredns/Corefile")
	}
	if got := files["coredns/zones/home.example.com.db"]; got != "ZONEDATA" {
		t.Errorf("zone db content = %q, want ZONEDATA", got)
	}
}
