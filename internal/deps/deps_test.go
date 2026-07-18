package deps

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stevenzg/tarjan/internal/config"
	"gopkg.in/yaml.v3"
)

func TestExtractVersion(t *testing.T) {
	cases := map[string]string{
		"git version 2.43.0":           "2.43.0",
		"v22.22.2":                     "22.22.2",
		"Docker version 25.0.3, build": "25.0.3",
		"go1.24.7":                     "1.24.7",
		"no numbers here":              "",
		// A copyright year printed before the real version must not win.
		"MyTool\nCopyright 2020.01 Acme\nversion 3.2.1": "3.2.1",
		// A bare single-integer version parses (so minVersion can enforce it).
		"toolchain version 4": "4",
		// No marker: fall back to the first dotted version in the banner.
		"aws-cli/2.13.0 Python/3.11.4": "2.13.0",
		// A stray "vN" earlier in the banner must not beat the real "version".
		"myapp (protocol v2) version 3.4.5": "3.4.5",
	}
	for in, want := range cases {
		if got := extractVersion(in); got != want {
			t.Errorf("extractVersion(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestVersionAtLeast(t *testing.T) {
	cases := []struct {
		got, want  string
		ok, usable bool
	}{
		{"22.22.2", "20", true, true},
		{"18.0.0", "20", false, true},
		{"20.0.0", "20", true, true},
		{"2.43.0", "2.43.1", false, true},
		{"2.43.0", "2.43", true, true},
		{"garbage", "20", false, false},
	}
	for _, c := range cases {
		ok, usable := versionAtLeast(c.got, c.want)
		if usable != c.usable || (usable && ok != c.ok) {
			t.Errorf("versionAtLeast(%q,%q) = (%v,%v), want (%v,%v)", c.got, c.want, ok, usable, c.ok, c.usable)
		}
	}
}

func TestCheckVerifiesWithoutPath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX check command")
	}
	// A tool whose name is not on PATH is still satisfied when its Check exits 0,
	// so a requirement can be something PATH cannot see (e.g. a shared library).
	present := config.Tool{Name: "some-lib", Check: "true"}
	if err := Check([]config.Tool{present}, Options{}); err != nil {
		t.Fatalf("check exiting 0 should satisfy the tool: %v", err)
	}

	// A failing Check marks the tool missing — and without --install that is an
	// error for a required tool.
	absent := config.Tool{Name: "some-lib", Check: "false"}
	if err := Check([]config.Tool{absent}, Options{}); err == nil {
		t.Fatal("check exiting non-zero should fail a required tool")
	}

	// The same failing tool only warns when optional.
	absentOpt := config.Tool{Name: "some-lib", Check: "false", Optional: true}
	if err := Check([]config.Tool{absentOpt}, Options{}); err != nil {
		t.Fatalf("optional tool with a failing check should not error: %v", err)
	}
}

func TestAutoInstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("uses a POSIX install script")
	}
	bin := t.TempDir()
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))

	// An install command that materialises the tool onto PATH.
	script := fmt.Sprintf(`printf '#!/bin/sh\necho 9.9.9\n' > %s/tarjanfaketool && chmod +x %s/tarjanfaketool`, bin, bin)
	y := fmt.Sprintf("name: tarjanfaketool\nversionCommand: tarjanfaketool\ninstall: %q\n", script)
	var tool config.Tool
	if err := yaml.Unmarshal([]byte(y), &tool); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Without opt-in it must fail (tool absent, no silent install).
	if err := Check([]config.Tool{tool}, Options{}); err == nil {
		t.Fatal("expected failure when tool missing and AutoInstall off")
	}
	// With opt-in it installs and then succeeds.
	if err := Check([]config.Tool{tool}, Options{AutoInstall: true}); err != nil {
		t.Fatalf("auto-install should satisfy the tool: %v", err)
	}
	if _, err := os.Stat(filepath.Join(bin, "tarjanfaketool")); err != nil {
		t.Fatalf("install command did not create the tool: %v", err)
	}
}
