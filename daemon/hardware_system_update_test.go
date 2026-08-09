package main

import "testing"

func TestCompareSemver(t *testing.T) {
	tests := []struct {
		name      string
		current   string
		remote    string
		want      int
		wantValid bool
	}{
		{name: "same prerelease", current: "0.9.3-alpha", remote: "0.9.3-alpha", want: 0, wantValid: true},
		{name: "new patch", current: "0.9.2-alpha", remote: "0.9.3-alpha", want: -1, wantValid: true},
		{name: "remote older", current: "0.9.3-alpha", remote: "0.9.2-alpha", want: 1, wantValid: true},
		{name: "stable after prerelease", current: "0.9.3-alpha", remote: "0.9.3", want: -1, wantValid: true},
		{name: "numeric prerelease", current: "1.0.0-alpha.2", remote: "1.0.0-alpha.10", want: -1, wantValid: true},
		{name: "v prefix", current: "v1.2.3", remote: "1.2.4", want: -1, wantValid: true},
		{name: "invalid", current: "dev", remote: "1.0.0", want: 0, wantValid: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, valid := compareSemver(tt.current, tt.remote)
			if valid != tt.wantValid || got != tt.want {
				t.Fatalf("compareSemver(%q, %q) = (%d, %v), want (%d, %v)", tt.current, tt.remote, got, valid, tt.want, tt.wantValid)
			}
		})
	}
}

func TestPackageVersion(t *testing.T) {
	version, err := packageVersion([]byte(`{"name":"nimos","version":"0.9.3-alpha"}`))
	if err != nil {
		t.Fatalf("packageVersion returned error: %v", err)
	}
	if version != "0.9.3-alpha" {
		t.Fatalf("packageVersion = %q", version)
	}

	if _, err := packageVersion([]byte(`{"version":"latest"}`)); err == nil {
		t.Fatal("packageVersion accepted an invalid semantic version")
	}
}
