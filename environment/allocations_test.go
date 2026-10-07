package environment

import (
	"testing"

	"github.com/docker/go-connections/nat"

	"github.com/pterodactyl/wings/config"
)

func configureNetwork(t *testing.T, ispn bool) {
	t.Helper()

	cfg, err := config.NewAtPath("")
	if err != nil {
		t.Fatal(err)
	}
	cfg.Docker.Network.Interface = "172.18.0.1"
	cfg.Docker.Network.ISPN = ispn
	config.Set(cfg)
}

func TestAllocationsDockerBindingsRewritesLocalhostBindings(t *testing.T) {
	configureNetwork(t, false)

	allocations := Allocations{
		Mappings: map[string][]int{
			"127.0.0.1": {25565, 25565},
		},
	}

	bindings := allocations.DockerBindings()
	for _, port := range []nat.Port{"25565/tcp", "25565/udp"} {
		got := bindings[port]
		if len(got) != 2 {
			t.Fatalf("expected two bindings for %s, got %d", port, len(got))
		}
		for _, binding := range got {
			if binding.HostIP != "172.18.0.1" {
				t.Fatalf("expected binding for %s to use interface IP, got %q", port, binding.HostIP)
			}
		}
	}
}

func TestAllocationsDockerBindingsDropsLocalhostBindingsForISPN(t *testing.T) {
	configureNetwork(t, true)

	allocations := Allocations{
		Mappings: map[string][]int{
			"127.0.0.1": {25565, 25565},
		},
	}

	bindings := allocations.DockerBindings()
	for _, port := range []nat.Port{"25565/tcp", "25565/udp"} {
		if got, ok := bindings[port]; ok {
			t.Fatalf("expected no bindings for %s, got %d", port, len(got))
		}
	}

	if exposed := allocations.Exposed(); len(exposed) != 0 {
		t.Fatalf("expected no exposed ports, got %d", len(exposed))
	}
}
