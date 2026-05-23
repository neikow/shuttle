package orchestrator

import (
	"reflect"
	"testing"
)

func TestRollingWarnings(t *testing.T) {
	tests := []struct {
		name    string
		compose string
		want    int // number of warnings
	}{
		{
			name:    "clean service, no host port",
			compose: "services:\n  app:\n    image: nginx\n    ports:\n      - \"80\"\n",
			want:    0,
		},
		{
			name:    "fixed host port short form",
			compose: "services:\n  app:\n    image: nginx\n    ports:\n      - \"8080:80\"\n",
			want:    1,
		},
		{
			name:    "fixed host port with ip",
			compose: "services:\n  app:\n    image: nginx\n    ports:\n      - \"127.0.0.1:8080:80\"\n",
			want:    1,
		},
		{
			name:    "ephemeral host port (ip::container)",
			compose: "services:\n  app:\n    image: nginx\n    ports:\n      - \"127.0.0.1::80\"\n",
			want:    0,
		},
		{
			name:    "long form published",
			compose: "services:\n  app:\n    image: nginx\n    ports:\n      - target: 80\n        published: 8080\n",
			want:    1,
		},
		{
			name:    "long form target only",
			compose: "services:\n  app:\n    image: nginx\n    ports:\n      - target: 80\n",
			want:    0,
		},
		{
			name:    "container_name set",
			compose: "services:\n  app:\n    image: nginx\n    container_name: myapp\n",
			want:    1,
		},
		{
			name:    "both problems",
			compose: "services:\n  app:\n    image: nginx\n    container_name: myapp\n    ports:\n      - \"8080:80\"\n",
			want:    2,
		},
		{
			name:    "unparseable compose",
			compose: "this: : is not : valid yaml: [",
			want:    0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := rollingWarnings([]byte(tt.compose))
			if len(got) != tt.want {
				t.Errorf("rollingWarnings() = %d warnings %v, want %d", len(got), got, tt.want)
			}
		})
	}
}

func TestRollingWarnings_recreateNotInspected(t *testing.T) {
	// rollingCheck (not exercised here) gates on policy; rollingWarnings itself
	// always inspects. Sanity-check the warning text is stable/sorted.
	got := rollingWarnings([]byte("services:\n  a:\n    container_name: x\n  b:\n    container_name: y\n"))
	want := []string{
		`compose service "a" sets container_name; rolling update cannot run two instances (use update_policy: recreate)`,
		`compose service "b" sets container_name; rolling update cannot run two instances (use update_policy: recreate)`,
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}
