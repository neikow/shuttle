package orchestrator

import (
	"reflect"
	"testing"

	"github.com/neikow/shuttle/internal/config"
)

func TestServicesMatching(t *testing.T) {
	svcs := []config.Service{
		{Name: "api", EnvFrom: "prod"},                         // base "/shared" + template "/services/api"
		{Name: "web", EnvFrom: "prod"},                         // base "/shared" + template "/services/web"
		{Name: "worker", EnvFrom: "staging"},                   // wrong env
		{Name: "billing", EnvFrom: "prod", SecretPath: "/pay"}, // explicit folder
		{Name: "noenv"},                                        // env_from empty -> defaultEnv
	}
	g := &GitSyncer{secretsBasePath: "/shared", secretsPathTemplate: "/services/{service}"}

	tests := []struct {
		name       string
		env, path  string
		defaultEnv string
		want       []string
	}{
		{"shared base hits all in env", "prod", "/shared", "prod", []string{"api", "billing", "noenv", "web"}},
		{"service folder hits one", "prod", "/services/web", "prod", []string{"web"}},
		{"explicit secret_path", "prod", "/pay", "prod", []string{"billing"}},
		{"wrong env no match", "staging", "/shared", "prod", []string{"worker"}},
		{"unrelated path none", "prod", "/nowhere", "prod", nil},
		{"trailing slash normalized", "prod", "/services/api/", "prod", []string{"api"}},
		{"empty env_from uses default", "prod", "/services/noenv", "prod", []string{"noenv"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := g.servicesMatching(svcs, tt.env, tt.path, tt.defaultEnv)
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("servicesMatching(%q,%q) = %v, want %v", tt.env, tt.path, got, tt.want)
			}
		})
	}
}

func TestSameFolder(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"/shared", "/shared", true},
		{"/shared/", "/shared", true},
		{"/", "/", true},
		{"/a", "/ab", false},
		{"/a", "/a/b", false}, // non-recursive: subfolder is a different folder
		{"", "/", true},
	}
	for _, c := range cases {
		if got := sameFolder(c.a, c.b); got != c.want {
			t.Errorf("sameFolder(%q,%q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}
