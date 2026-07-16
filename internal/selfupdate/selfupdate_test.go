package selfupdate

import "testing"

func TestIsNewer(t *testing.T) {
	cases := []struct {
		current, latest string
		want            bool
	}{
		{"0.4.0", "0.5.0", true},
		{"v0.4.0", "v0.4.1", true},
		{"0.4.0", "1.0.0", true},
		{"0.4.0", "0.4.0", false},
		{"0.5.0", "0.4.0", false},
		{"1.0.0", "0.9.9", false},
		{"0.4.0", "0.4.0-rc1", false}, // same release, prerelease suffix ignored
		{"dev", "0.4.0", false},       // dev build is never "out of date"
		{"0.4.0", "garbage", false},   // unparseable latest
		{"0.10.0", "0.9.0", false},    // numeric, not lexical, comparison
		{"0.9.0", "0.10.0", true},
	}
	for _, c := range cases {
		if got := IsNewer(c.current, c.latest); got != c.want {
			t.Errorf("IsNewer(%q, %q) = %v, want %v", c.current, c.latest, got, c.want)
		}
	}
}

func TestParseable(t *testing.T) {
	for _, v := range []string{"0.4.0", "v1.2.3", "0.4.0-rc1"} {
		if !Parseable(v) {
			t.Errorf("Parseable(%q) = false, want true", v)
		}
	}
	for _, v := range []string{"dev", "", "1.2", "x.y.z"} {
		if Parseable(v) {
			t.Errorf("Parseable(%q) = true, want false", v)
		}
	}
}

func TestChecksumFor(t *testing.T) {
	sums := "aaa  tarjan_0.4.0_linux_amd64.tar.gz\n" +
		"bbb  tarjan_0.4.0_darwin_arm64.tar.gz\n"
	if got := checksumFor(sums, "tarjan_0.4.0_darwin_arm64.tar.gz"); got != "bbb" {
		t.Errorf("checksumFor = %q, want bbb", got)
	}
	if got := checksumFor(sums, "tarjan_0.4.0_windows_amd64.zip"); got != "" {
		t.Errorf("checksumFor for missing asset = %q, want empty", got)
	}
}

func TestAssetName(t *testing.T) {
	// Tag with a leading v must still produce a bare-version asset name.
	name, isZip := assetName("v0.4.0")
	if isZip {
		if want := "tarjan_0.4.0_windows_"; name[:len(want)] != want {
			t.Errorf("zip asset name = %q", name)
		}
	} else if want := "tarjan_0.4.0_"; name[:len(want)] != want {
		t.Errorf("asset name = %q, want prefix %q", name, want)
	}
}
