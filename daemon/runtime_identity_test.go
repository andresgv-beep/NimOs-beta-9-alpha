package main

import "testing"

func TestRuntimeIdentityEnv(t *testing.T) {
	cases := []struct {
		name string
		rt   *runtimeIdentity
		uid  int
		gid  int
		want map[string]string
	}{
		{"nil → vacío", nil, 100021, 100021, map[string]string{}},
		{"gitea USER_UID/USER_GID", &runtimeIdentity{UIDEnv: "USER_UID", GIDEnv: "USER_GID"}, 100021, 100021,
			map[string]string{"USER_UID": "100021", "USER_GID": "100021"}},
		{"linuxserver PUID/PGID", &runtimeIdentity{UIDEnv: "PUID", GIDEnv: "PGID"}, 100006, 100006,
			map[string]string{"PUID": "100006", "PGID": "100006"}},
		{"synapse UID/GID con números distintos", &runtimeIdentity{UIDEnv: "UID", GIDEnv: "GID"}, 100001, 100002,
			map[string]string{"UID": "100001", "GID": "100002"}},
		{"solo uidEnv", &runtimeIdentity{UIDEnv: "USER_UID"}, 100021, 100021,
			map[string]string{"USER_UID": "100021"}},
		{"espacios se recortan", &runtimeIdentity{UIDEnv: "  USER_UID  ", GIDEnv: " USER_GID "}, 5, 5,
			map[string]string{"USER_UID": "5", "USER_GID": "5"}},
		{"nombres vacíos → nada", &runtimeIdentity{UIDEnv: "", GIDEnv: ""}, 100021, 100021,
			map[string]string{}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := runtimeIdentityEnv(tc.rt, tc.uid, tc.gid)
			if len(got) != len(tc.want) {
				t.Fatalf("len distinto: got %v, want %v", got, tc.want)
			}
			for k, v := range tc.want {
				if got[k] != v {
					t.Fatalf("clave %q: got %q, want %q (completo: %v)", k, got[k], v, got)
				}
			}
		})
	}
}

func TestParseRuntimeIdentity(t *testing.T) {
	// Presente y completo.
	body := map[string]interface{}{
		"runtimeIdentity": map[string]interface{}{"uidEnv": "USER_UID", "gidEnv": "USER_GID"},
	}
	rt := parseRuntimeIdentity(body)
	if rt == nil || rt.UIDEnv != "USER_UID" || rt.GIDEnv != "USER_GID" {
		t.Fatalf("esperaba USER_UID/USER_GID, got %+v", rt)
	}

	// Ausente → nil (app que no necesita inyección).
	if parseRuntimeIdentity(map[string]interface{}{}) != nil {
		t.Fatal("body sin runtimeIdentity debería dar nil")
	}

	// Presente pero vacío → nil.
	empty := map[string]interface{}{"runtimeIdentity": map[string]interface{}{}}
	if parseRuntimeIdentity(empty) != nil {
		t.Fatal("runtimeIdentity vacío debería dar nil")
	}

	// Solo uidEnv → válido (no nil).
	onlyUID := map[string]interface{}{"runtimeIdentity": map[string]interface{}{"uidEnv": "PUID"}}
	if rt := parseRuntimeIdentity(onlyUID); rt == nil || rt.UIDEnv != "PUID" || rt.GIDEnv != "" {
		t.Fatalf("esperaba PUID solo, got %+v", rt)
	}
}
