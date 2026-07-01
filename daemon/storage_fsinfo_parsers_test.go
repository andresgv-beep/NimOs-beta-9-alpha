package main

import "testing"

func TestParseDeviceStats(t *testing.T) {
	out := `[/dev/sda].write_io_errs    22
[/dev/sda].read_io_errs     0
[/dev/sda].flush_io_errs    3
[/dev/sda].corruption_errs  0
[/dev/sda].generation_errs  0
[/dev/sdb].write_io_errs    0
[/dev/sdb].read_io_errs     5`

	got := parseDeviceStats(out)

	if got["/dev/sda"].write != 22 {
		t.Errorf("sda write: got %d, want 22", got["/dev/sda"].write)
	}
	if got["/dev/sda"].flush != 3 {
		t.Errorf("sda flush: got %d, want 3", got["/dev/sda"].flush)
	}
	if got["/dev/sdb"].read != 5 {
		t.Errorf("sdb read: got %d, want 5", got["/dev/sdb"].read)
	}
	if got["/dev/sdb"].write != 0 {
		t.Errorf("sdb write: got %d, want 0", got["/dev/sdb"].write)
	}
}

func TestParseTrailingInt(t *testing.T) {
	cases := map[string]int64{
		"Device size:		  1000204886016":  1000204886016,
		"Used:			   123456789":           123456789,
		"no number here":                 0,
		"[/dev/sda].write_io_errs    22": 22,
	}
	for in, want := range cases {
		if got := parseTrailingInt(in); got != want {
			t.Errorf("parseTrailingInt(%q): got %d, want %d", in, got, want)
		}
	}
}

func TestParseFsDevidLine(t *testing.T) {
	dev := parseFsDevidLine("devid    1 size 111.79GiB used 5.02GiB path /dev/sda")
	if dev.DeviceID != 1 {
		t.Errorf("DeviceID: got %d, want 1", dev.DeviceID)
	}
	if dev.DevicePath != "/dev/sda" {
		t.Errorf("DevicePath: got %q, want /dev/sda", dev.DevicePath)
	}

	// Línea sin path → DevicePath vacío (se descarta aguas arriba)
	empty := parseFsDevidLine("devid 2 size 100GiB used 1GiB")
	if empty.DevicePath != "" {
		t.Errorf("expected empty DevicePath, got %q", empty.DevicePath)
	}
}
