// maintenance_network_prune_test.go — tests de la lógica pura de poda de redes.

package main

import "testing"

func TestParseDockerNetworkPruneOutput(t *testing.T) {
	cases := []struct {
		name string
		out  string
		want int
	}{
		{
			name: "varias redes borradas",
			out:  "Deleted Networks:\nmealie_default\nlidarr_default\njackett_default\n",
			want: 3,
		},
		{
			name: "una sola red",
			out:  "Deleted Networks:\nmealie_default\n",
			want: 1,
		},
		{
			name: "nada que borrar (salida vacía)",
			out:  "",
			want: 0,
		},
		{
			name: "solo cabecera sin redes",
			out:  "Deleted Networks:\n",
			want: 0,
		},
		{
			name: "líneas en blanco intercaladas no cuentan",
			out:  "Deleted Networks:\n\nmealie_default\n\nlidarr_default\n\n",
			want: 2,
		},
		{
			name: "ruido antes de la cabecera no cuenta",
			out:  "WARNING: This will remove all networks not used\nDeleted Networks:\nfoo_default\n",
			want: 1,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := parseDockerNetworkPruneOutput(c.out); got != c.want {
				t.Errorf("parseDockerNetworkPruneOutput(%q) = %d, want %d", c.out, got, c.want)
			}
		})
	}
}

// La tarea debe estar en la categoría Docker (subcategoría de la UI).
func TestDockerNetworkPruneTask_Category(t *testing.T) {
	task := &dockerNetworkPruneTask{}
	if task.Category() != MaintCategoryDocker {
		t.Errorf("Category() = %q, want %q", task.Category(), MaintCategoryDocker)
	}
	if task.ID() != "docker_network_prune" {
		t.Errorf("ID() = %q, want docker_network_prune", task.ID())
	}
}
