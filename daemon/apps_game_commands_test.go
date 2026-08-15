package main

import "testing"

func TestGameQuickCommandsFromConfig(t *testing.T) {
	tests := []struct {
		name   string
		config string
		appID  string
		want   []GameQuickCommand
	}{
		{
			name:   "declared commands are returned",
			appID:  "project-zomboid",
			config: `{"game":{"quickCommands":[{"label":"Guardar mundo","command":"save"},{"label":"Jugadores","command":"players"}]}}`,
			want: []GameQuickCommand{
				{Label: "Guardar mundo", Command: "save"},
				{Label: "Jugadores", Command: "players"},
			},
		},
		{
			name:   "invalid and empty entries are ignored",
			appID:  "project-zomboid",
			config: `{"game":{"quickCommands":[{"label":"Válido","command":" help "},{"label":"","command":"save"},"invalid"]}}`,
			want:   []GameQuickCommand{{Label: "Válido", Command: "help"}},
		},
		{
			name:   "legacy minecraft keeps historical commands",
			appID:  "minecraft-java",
			config: `{}`,
			want: []GameQuickCommand{
				{Label: "Jugadores", Command: "list"},
				{Label: "Hacer de día", Command: "time set day"},
				{Label: "Despejar clima", Command: "weather clear"},
				{Label: "Guardar mundo", Command: "save-all"},
			},
		},
		{
			name:   "legacy project zomboid gets its own commands",
			appID:  "project-zomboid",
			config: `{}`,
			want: []GameQuickCommand{
				{Label: "Jugadores", Command: "players"},
				{Label: "Guardar mundo", Command: "save"},
				{Label: "Ver opciones", Command: "showoptions"},
				{Label: "Ayuda", Command: "help"},
			},
		},
		{
			name:   "unknown game gets no minecraft commands",
			appID:  "other-game",
			config: `{}`,
			want:   []GameQuickCommand{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := gameQuickCommandsFromConfig(tt.config, tt.appID)
			if len(got) != len(tt.want) {
				t.Fatalf("got %d commands, want %d: %#v", len(got), len(tt.want), got)
			}
			for i := range tt.want {
				if got[i] != tt.want[i] {
					t.Errorf("command %d = %#v, want %#v", i, got[i], tt.want[i])
				}
			}
		})
	}
}
